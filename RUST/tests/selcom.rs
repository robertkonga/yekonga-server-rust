//! The built-in Selcom payment provider and its webhook, against a local mock.

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use axum::body::Body;
use axum::extract::{RawQuery, State};
use axum::routing::{get, post};
use axum::Router;
use base64::Engine;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::payment::{PaymentRequest, PushRequest};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// Records the signed headers the mock Selcom server received.
#[derive(Default)]
struct Captured {
    headers: Vec<(String, String)>,
}

fn b64(value: &str) -> String {
    base64::engine::general_purpose::STANDARD.encode(value)
}

/// A mock Selcom API: checkout create/status, wallet push, and C2B status.
async fn mock_selcom() -> (SocketAddr, Arc<Mutex<Captured>>) {
    let captured = Arc::new(Mutex::new(Captured::default()));

    async fn create(
        State(captured): State<Arc<Mutex<Captured>>>,
        headers: http::HeaderMap,
        _body: String,
    ) -> ([(&'static str, &'static str); 1], String) {
        capture(&captured, &headers);
        (
            [("content-type", "application/json")],
            json!({
                "reference": "SEL-REF-1",
                "resultcode": "000",
                "result": "SUCCESS",
                "data": [{"payment_gateway_url": b64("https://pay.selcom.co.tz/checkout/abc")}]
            })
            .to_string(),
        )
    }

    async fn push() -> ([(&'static str, &'static str); 1], String) {
        (
            [("content-type", "application/json")],
            json!({"reference": "SEL-REF-2", "resultcode": "000", "message": "Enter your PIN"})
                .to_string(),
        )
    }

    // Echoes the queried order_id so the caller's reference round-trips.
    async fn order_status(
        RawQuery(query): RawQuery,
    ) -> ([(&'static str, &'static str); 1], String) {
        let query = query.unwrap_or_default();
        let order_id = query
            .split('&')
            .find_map(|pair| pair.strip_prefix("order_id="))
            .map(|v| v.replace("%2F", "/"))
            .unwrap_or_default();
        (
            [("content-type", "application/json")],
            json!({
                "reference": "SEL-REF-1",
                "resultcode": "000",
                "data": [{
                    "order_id": order_id,
                    "reference": "SEL-REF-1",
                    "transid": "TXN-1",
                    "payment_status": "COMPLETED",
                    "amount": 5000,
                    "channel": "TIGO-TZ"
                }]
            })
            .to_string(),
        )
    }

    let app = Router::new()
        .route("/v1/checkout/create-order-minimal", post(create))
        .route("/v1/checkout/order-status", get(order_status))
        .route("/v1/wallet/pushussd", post(push))
        .with_state(captured.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (addr, captured)
}

fn capture(captured: &Arc<Mutex<Captured>>, headers: &http::HeaderMap) {
    let mut slot = captured.lock().unwrap();
    slot.headers = headers
        .iter()
        .map(|(k, v)| {
            (
                k.as_str().to_string(),
                v.to_str().unwrap_or_default().to_string(),
            )
        })
        .collect();
}

fn app_with_selcom(base_url: &str) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "hasPaymentModule": true,
        "apiGateway": {"payment": {"providers": [{
            "provider": "selcom",
            "baseURL": base_url,
            "credentials": {"api_key": "key-1", "api_secret": "secret-1", "vendor": "VENDOR01"},
        }]}}
    }))
    .unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

fn header(captured: &Arc<Mutex<Captured>>, name: &str) -> String {
    captured
        .lock()
        .unwrap()
        .headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case(name))
        .map(|(_, v)| v.clone())
        .unwrap_or_default()
}

#[tokio::test]
async fn create_payment_returns_the_checkout_url() {
    let (addr, captured) = mock_selcom().await;
    let app = app_with_selcom(&format!("http://{addr}"));

    let response = app
        .create_payment(
            "selcom",
            PaymentRequest {
                reference: "pay-1".into(),
                amount: 5000.0,
                currency: "TZS".into(),
                customer: yekonga::payment::Customer {
                    name: "Ally".into(),
                    email: "ally@shop.tz".into(),
                    phone: "+255 712-345678".into(),
                    ..Default::default()
                },
                return_url: "https://shop.tz/return".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();

    assert_eq!(response.provider_ref, "SEL-REF-1");
    assert_eq!(
        response.checkout_url,
        "https://pay.selcom.co.tz/checkout/abc"
    );
    assert_eq!(response.status, "PENDING");

    // The request is signed: Authorization is base64 of the api key, and the
    // digest/signed-fields headers are present.
    assert_eq!(
        header(&captured, "authorization"),
        format!("SELCOM {}", b64("key-1"))
    );
    assert_eq!(header(&captured, "digest-method"), "HS256");
    assert!(!header(&captured, "digest").is_empty());
    assert!(header(&captured, "signed-fields").starts_with("vendor,order_id,"));
}

#[tokio::test]
async fn push_ussd_prompts_the_customer() {
    let (addr, _) = mock_selcom().await;
    let app = app_with_selcom(&format!("http://{addr}"));

    let response = app
        .push_ussd(
            "selcom",
            PushRequest {
                reference: "pay-2".into(),
                amount: 1000.0,
                currency: "TZS".into(),
                phone: "255713000000".into(),
                description: "Order".into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(response.provider_ref, "SEL-REF-2");
    assert_eq!(response.status, "PENDING");
    assert_eq!(response.instructions, "Enter your PIN");
}

#[tokio::test]
async fn verify_payment_reads_the_order_status() {
    let (addr, _) = mock_selcom().await;
    let app = app_with_selcom(&format!("http://{addr}"));

    let result = app.verify_payment("selcom", "pay-1").await.unwrap();
    assert_eq!(result.status, "SUCCESS");
    assert_eq!(result.reference, "pay-1");
    assert_eq!(result.provider_payment_id, "TXN-1");
    assert_eq!(result.currency, "TZS");
    assert_eq!(result.method, "TIGO-TZ");
}

#[tokio::test]
async fn a_callback_settles_the_payment_via_order_status() {
    let (addr, _) = mock_selcom().await;
    let app = app_with_selcom(&format!("http://{addr}"));

    let record = app
        .query("Payment")
        .unwrap()
        .create(json!({
            "provider": "selcom",
            "providerConfig": "default",
            "status": "PENDING",
            "amount": 5000.0,
            "currency": "TZS",
        }))
        .await
        .unwrap();
    let id = record["id"].as_str().unwrap().to_string();

    // Selcom's unsigned callback only carries the order id; the framework's
    // built-in provider re-queries the order status to authenticate it.
    let body = json!({"order_id": id}).to_string();
    let request = HttpRequest::post("/payment/webhook/selcom")
        .header("content-type", "application/json")
        .header("host", "localhost")
        .body(Body::from(body))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status().as_u16(), 200);
    let _ = response.into_body().collect().await.unwrap();

    let updated = app
        .query("Payment")
        .unwrap()
        .skip_tenant()
        .where_("id", id.as_str())
        .find_one()
        .await
        .unwrap()
        .unwrap();
    assert_eq!(updated["status"], "SUCCESS");
    assert_eq!(updated["providerPaymentId"], "TXN-1");
}

#[tokio::test]
async fn a_callback_without_order_id_is_rejected() {
    let (addr, _) = mock_selcom().await;
    let app = app_with_selcom(&format!("http://{addr}"));

    let request = HttpRequest::post("/payment/webhook/selcom")
        .header("content-type", "application/json")
        .header("host", "localhost")
        .body(Body::from(json!({"foo": "bar"}).to_string()))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status().as_u16(), 401);
    let _: Value =
        serde_json::from_slice(&response.into_body().collect().await.unwrap().to_bytes())
            .unwrap_or(Value::Null);
}
