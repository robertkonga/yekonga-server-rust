//! The payment webhook route with a pluggable verifier.

use std::sync::Arc;

use axum::body::Body;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::payment::{PaymentResult, WebhookError, WebhookEvent, STATUS_SUCCEEDED};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// An app with the payment module on (so the Payment model and webhook routes
/// exist).
fn app() -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "secret"},
        "hasPaymentModule": true
    }))
    .unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

/// Registers a verifier that trusts the JSON body: it builds the event from
/// `reference`/`status`/`amount`/`currency`, echoing the shape a real provider
/// verifier would return after checking a signature.
fn register_trusting_verifier(app: &Yekonga) {
    app.set_payment_verify(|ctx| {
        Box::pin(async move {
            let body: Value =
                serde_json::from_slice(&ctx.body).map_err(|_| WebhookError::InvalidRequest)?;
            Ok(WebhookEvent {
                provider: ctx.provider,
                event_type: "payment.updated".into(),
                result: Some(PaymentResult {
                    reference: body["reference"].as_str().unwrap_or_default().into(),
                    status: body["status"].as_str().unwrap_or(STATUS_SUCCEEDED).into(),
                    amount: body["amount"].as_f64().unwrap_or(0.0),
                    currency: body["currency"].as_str().unwrap_or_default().into(),
                    provider_payment_id: "capture-1".into(),
                    method: "card".into(),
                    raw: body,
                    ..Default::default()
                }),
                ack: Some(json!({"received": true})),
            })
        })
    });
}

async fn create_payment(app: &Yekonga, amount: f64) -> String {
    let record = app
        .query("Payment")
        .unwrap()
        .create(json!({
            "provider": "demo",
            "providerConfig": "default",
            "status": "PENDING",
            "amount": amount,
            "currency": "TZS",
        }))
        .await
        .unwrap();
    record["id"].as_str().unwrap().to_string()
}

async fn post_webhook(app: &Yekonga, provider: &str, body: Value) -> (u16, Value) {
    let request = HttpRequest::post(format!("/payment/webhook/{provider}"))
        .header("content-type", "application/json")
        .header("host", "localhost")
        .body(Body::from(body.to_string()))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    let status = response.status().as_u16();
    let bytes = response.into_body().collect().await.unwrap().to_bytes();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}

async fn payment_status(app: &Yekonga, id: &str) -> DataMapView {
    let record = app
        .query("Payment")
        .unwrap()
        .skip_tenant()
        .where_("id", id)
        .find_one()
        .await
        .unwrap()
        .unwrap();
    DataMapView(record)
}

struct DataMapView(yekonga::DataMap);
impl DataMapView {
    fn str(&self, key: &str) -> String {
        self.0
            .get(key)
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string()
    }
}

#[tokio::test]
async fn webhook_settles_a_matching_payment() {
    let app = app();
    register_trusting_verifier(&app);
    let id = create_payment(&app, 100.0).await;

    let (status, ack) = post_webhook(
        &app,
        "demo",
        json!({"reference": id, "status": "SUCCESS", "amount": 100.0, "currency": "TZS"}),
    )
    .await;

    assert_eq!(status, 200);
    assert_eq!(ack, json!({"received": true}));

    let record = payment_status(&app, &id).await;
    assert_eq!(record.str("status"), "SUCCESS");
    assert_eq!(record.str("providerPaymentId"), "capture-1");
    assert_eq!(record.str("paymentMethod"), "card");
    assert!(!record.str("paidAt").is_empty(), "paidAt should be set");
}

#[tokio::test]
async fn a_success_with_the_wrong_amount_is_recorded_as_failed() {
    let app = app();
    register_trusting_verifier(&app);
    let id = create_payment(&app, 100.0).await;

    // Provider reports a success for a different amount than the invoice.
    let (status, _) = post_webhook(
        &app,
        "demo",
        json!({"reference": id, "status": "SUCCESS", "amount": 5.0, "currency": "TZS"}),
    )
    .await;
    assert_eq!(status, 200);

    let record = payment_status(&app, &id).await;
    assert_eq!(record.str("status"), "FAILED");
    assert!(record.str("failureReason").contains("amount mismatch"));
}

#[tokio::test]
async fn a_settled_payment_never_moves_back() {
    let app = app();
    register_trusting_verifier(&app);
    let id = create_payment(&app, 100.0).await;

    for body in [
        json!({"reference": id, "status": "SUCCESS", "amount": 100.0, "currency": "TZS"}),
        json!({"reference": id, "status": "FAILED", "amount": 100.0, "currency": "TZS"}),
    ] {
        assert_eq!(post_webhook(&app, "demo", body).await.0, 200);
    }

    // The later FAILED event must not undo the settled payment.
    assert_eq!(payment_status(&app, &id).await.str("status"), "SUCCESS");
}

#[tokio::test]
async fn a_webhook_without_a_verifier_is_refused() {
    let app = app();
    // No set_payment_verify registered.
    let (status, _) = post_webhook(&app, "demo", json!({"reference": "x"})).await;
    assert_eq!(status, 501);
}

#[tokio::test]
async fn a_rejected_webhook_returns_the_verifier_status() {
    let app = app();
    app.set_payment_verify(|_ctx| Box::pin(async { Err(WebhookError::InvalidWebhook) }));

    let (status, _) = post_webhook(&app, "demo", json!({"reference": "x"})).await;
    assert_eq!(status, 401);
}
