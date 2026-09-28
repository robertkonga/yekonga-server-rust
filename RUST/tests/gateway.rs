//! The built-in Beem SMS provider, exercised against a local mock server.

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use axum::extract::State;
use axum::routing::post;
use axum::Router;
use http::HeaderMap;
use serde_json::{json, Value};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// What the mock gateway received.
#[derive(Default)]
struct Captured {
    authorization: String,
    body: Value,
}

/// Starts a mock Beem server that records the request and replies with a
/// success (`code: 100`). Returns its address and the capture slot.
async fn mock_gateway() -> (SocketAddr, Arc<Mutex<Captured>>) {
    let captured = Arc::new(Mutex::new(Captured::default()));
    let state = captured.clone();

    async fn handler(
        State(captured): State<Arc<Mutex<Captured>>>,
        headers: HeaderMap,
        body: String,
    ) -> ([(&'static str, &'static str); 1], String) {
        let mut slot = captured.lock().unwrap();
        slot.authorization = headers
            .get("authorization")
            .and_then(|v| v.to_str().ok())
            .unwrap_or_default()
            .to_string();
        slot.body = serde_json::from_str(&body).unwrap_or(Value::Null);
        (
            [("content-type", "application/json")],
            json!({"code": 100, "message": "sent", "request_id": "req-1"}).to_string(),
        )
    }

    let app = Router::new()
        .route("/v1/send", post(handler))
        .with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (addr, captured)
}

fn app_with_sms(addr: SocketAddr) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "apiGateway": {"sms": {
            "provider": "beem",
            "baseURL": format!("http://{addr}"),
            "sender": "MYAPP",
            "apiKey": "k",
            "secretKey": "s3cret",
        }}
    }))
    .unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

#[tokio::test]
async fn beem_sends_an_sms() {
    let (addr, captured) = mock_gateway().await;
    let app = app_with_sms(addr);

    let response = app.send_sms_builtin("255712345678", "hello there").await;
    assert_eq!(response.status, "SUCCESS");
    assert_eq!(response.code, 100);
    assert_eq!(response.message_id, "req-1");

    let sent = captured.lock().unwrap();
    // Basic auth is base64("apiKey:secretKey").
    assert_eq!(sent.authorization, "Basic azpzM2NyZXQ=");
    assert_eq!(sent.body["source_addr"], "MYAPP");
    assert_eq!(sent.body["message"], "hello there");
    assert_eq!(sent.body["recipients"][0]["dest_addr"], "255712345678");
}

#[tokio::test]
async fn unknown_provider_fails() {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "apiGateway": {"sms": {"provider": "carrier-pigeon", "apiKey": "k"}}
    }))
    .unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );
    let response = app.send_sms_builtin("255712345678", "hi").await;
    assert_eq!(response.status, "FAILED");
    assert!(response.message.contains("not supported"));
}

#[tokio::test]
async fn notification_dispatch_uses_the_builtin_sms() {
    let (addr, captured) = mock_gateway().await;
    let app = app_with_sms(addr);

    app.notify(
        &yekonga::notify::NotifiedUser {
            user_id: "u1".into(),
            phone: "0712345678".into(),
            ..Default::default()
        },
        &yekonga::notify::NotificationParams {
            text: "queued sms".into(),
            ..Default::default()
        },
    )
    .await;
    app.deliver_notifications_now().await;

    let sent = captured.lock().unwrap();
    assert_eq!(sent.body["message"], "queued sms");
    assert_eq!(sent.body["recipients"][0]["dest_addr"], "255712345678");
}
