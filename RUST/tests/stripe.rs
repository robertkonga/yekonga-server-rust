//! The built-in Stripe payment provider and its webhook, exercised against a
//! local mock and a locally-signed webhook.

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use axum::body::Body;
use axum::extract::State;
use axum::routing::{get, post};
use axum::Router;
use hmac::{Hmac, Mac};
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::json;
use sha2::Sha256;
use tower::ServiceExt;
use yekonga::payment::{PaymentRequest, RefundRequest};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// Records the last form the mock Stripe server received.
#[derive(Default)]
struct Captured {
    authorization: String,
    form: String,
}

/// A mock Stripe API: `/checkout/sessions` (create + read) and `/refunds`.
async fn mock_stripe() -> (SocketAddr, Arc<Mutex<Captured>>) {
    let captured = Arc::new(Mutex::new(Captured::default()));

    async fn create(
        State(captured): State<Arc<Mutex<Captured>>>,
        headers: http::HeaderMap,
        body: String,
    ) -> ([(&'static str, &'static str); 1], String) {
        let mut slot = captured.lock().unwrap();
        slot.authorization = headers
            .get("authorization")
            .and_then(|v| v.to_str().ok())
            .unwrap_or_default()
            .into();
        slot.form = body;
        (
            [("content-type", "application/json")],
            json!({"id": "cs_test_1", "url": "https://checkout.stripe.com/c/pay/cs_test_1"})
                .to_string(),
        )
    }

    async fn read() -> ([(&'static str, &'static str); 1], String) {
        (
            [("content-type", "application/json")],
            json!({
                "id": "cs_test_1",
                "client_reference_id": "pay-1",
                "status": "complete",
                "payment_status": "paid",
                "amount_total": 5000,
                "currency": "usd",
                "payment_intent": "pi_test_1"
            })
            .to_string(),
        )
    }

    async fn refund(body: String) -> ([(&'static str, &'static str); 1], String) {
        let _ = body;
        (
            [("content-type", "application/json")],
            json!({"id": "re_test_1", "status": "succeeded"}).to_string(),
        )
    }

    let app = Router::new()
        .route("/checkout/sessions", post(create))
        .route("/checkout/sessions/{id}", get(read))
        .route("/refunds", post(refund))
        .with_state(captured.clone());
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (addr, captured)
}

fn app_with_stripe(base_url: &str) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "hasPaymentModule": true,
        "apiGateway": {"payment": {"providers": [{
            "provider": "stripe",
            "baseURL": base_url,
            "credentials": {"secret_key": "sk_test_123", "webhook_secret": "whsec_test"},
        }]}}
    }))
    .unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

#[tokio::test]
async fn create_payment_opens_a_checkout_session() {
    let (addr, captured) = mock_stripe().await;
    let app = app_with_stripe(&format!("http://{addr}"));

    let response = app
        .create_payment(
            "stripe",
            PaymentRequest {
                reference: "pay-1".into(),
                amount: 50.0,
                currency: "USD".into(),
                description: "Order pay-1".into(),
                return_url: "https://shop.tz/return".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();

    assert_eq!(response.provider_ref, "cs_test_1");
    assert_eq!(
        response.checkout_url,
        "https://checkout.stripe.com/c/pay/cs_test_1"
    );
    assert_eq!(response.status, "PENDING");

    let sent = captured.lock().unwrap();
    assert_eq!(sent.authorization, "Bearer sk_test_123");
    // Amount is sent in minor units (cents) for a 2-decimal currency
    // (the field name is URL-encoded: `unit_amount%5D=5000`).
    assert!(sent.form.contains("unit_amount%5D=5000"), "{}", sent.form);
    assert!(
        sent.form.contains("client_reference_id=pay-1"),
        "{}",
        sent.form
    );
}

#[tokio::test]
async fn verify_payment_reads_the_session() {
    let (addr, _) = mock_stripe().await;
    let app = app_with_stripe(&format!("http://{addr}"));

    let result = app.verify_payment("stripe", "cs_test_1").await.unwrap();
    assert_eq!(result.status, "SUCCESS");
    assert_eq!(result.reference, "pay-1");
    assert_eq!(result.provider_payment_id, "pi_test_1");
    assert_eq!(result.amount, 50.0);
    assert_eq!(result.currency, "USD");
}

#[tokio::test]
async fn refund_settles_through_the_api() {
    let (addr, _) = mock_stripe().await;
    let app = app_with_stripe(&format!("http://{addr}"));

    let result = app
        .refund(
            "stripe",
            RefundRequest {
                provider_payment_id: "pi_test_1".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(result.provider_refund_id, "re_test_1");
    assert_eq!(result.status, "REFUNDED");
}

/// Builds a valid `Stripe-Signature` header for `body` with `secret`.
fn stripe_signature(body: &str, secret: &str) -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).unwrap();
    mac.update(format!("{timestamp}.{body}").as_bytes());
    format!(
        "t={timestamp},v1={}",
        hex::encode(mac.finalize().into_bytes())
    )
}

async fn create_payment_record(app: &Yekonga) -> String {
    let record = app
        .query("Payment")
        .unwrap()
        .create(json!({
            "provider": "stripe",
            "providerConfig": "default",
            "status": "PENDING",
            "amount": 50.0,
            "currency": "USD",
        }))
        .await
        .unwrap();
    record["id"].as_str().unwrap().to_string()
}

#[tokio::test]
async fn a_signed_webhook_settles_the_payment() {
    let app = app_with_stripe("https://api.stripe.com/v1");
    let id = create_payment_record(&app).await;

    let body = json!({
        "type": "checkout.session.completed",
        "data": {"object": {
            "id": "cs_test_1",
            "client_reference_id": id,
            "status": "complete",
            "payment_status": "paid",
            "amount_total": 5000,
            "currency": "usd",
            "payment_intent": "pi_test_1"
        }}
    })
    .to_string();
    let signature = stripe_signature(&body, "whsec_test");

    let request = HttpRequest::post("/payment/webhook/stripe")
        .header("content-type", "application/json")
        .header("stripe-signature", signature)
        .header("host", "localhost")
        .body(Body::from(body))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status().as_u16(), 200);
    let _ = response.into_body().collect().await.unwrap();

    let record = app
        .query("Payment")
        .unwrap()
        .skip_tenant()
        .where_("id", id.as_str())
        .find_one()
        .await
        .unwrap()
        .unwrap();
    assert_eq!(record["status"], "SUCCESS");
    assert_eq!(record["providerPaymentId"], "pi_test_1");
}

#[tokio::test]
async fn a_webhook_with_a_bad_signature_is_rejected() {
    let app = app_with_stripe("https://api.stripe.com/v1");
    let _ = create_payment_record(&app).await;

    let body = json!({"type": "checkout.session.completed", "data": {"object": {}}}).to_string();
    let request = HttpRequest::post("/payment/webhook/stripe")
        .header("content-type", "application/json")
        .header("stripe-signature", "t=1,v1=deadbeef")
        .header("host", "localhost")
        .body(Body::from(body))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status().as_u16(), 401);
}
