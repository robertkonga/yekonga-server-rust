//! The built-in Beem SMS provider, exercised against a local mock server.

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};

use axum::body::Body;
use axum::extract::State;
use axum::routing::post;
use axum::Router;
use http::HeaderMap;
use http::Request as HttpRequest;
use http_body_util::BodyExt;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

/// What the mock gateway received.
#[derive(Default)]
struct Captured {
    authorization: String,
    body: Value,
    message_type: String,
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

/// A mock Infobip SMS server (`/sms/2/text/advanced`) that records the request
/// and replies with an accepted message.
async fn mock_infobip_sms() -> (SocketAddr, Arc<Mutex<Captured>>) {
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
            json!({"messages": [{"messageId": "sms-1", "status": {"description": "Message sent to next instance"}}]})
                .to_string(),
        )
    }

    let app = Router::new()
        .route("/sms/2/text/advanced", post(handler))
        .with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (addr, captured)
}

#[tokio::test]
async fn infobip_sends_an_sms() {
    let (addr, captured) = mock_infobip_sms().await;
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "apiGateway": {"sms": {
            "provider": "infobip",
            "baseURL": format!("http://{addr}"),
            "sender": "MYAPP",
            "apiKey": "smskey",
        }}
    }))
    .unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );

    let response = app.send_sms_builtin("255712345678", "karibu").await;
    assert_eq!(response.status, "SUCCESS", "{}", response.message);
    assert_eq!(response.message_id, "sms-1");

    let sent = captured.lock().unwrap();
    assert_eq!(sent.authorization, "App smskey");
    let message = &sent.body["messages"][0];
    assert_eq!(message["from"], "MYAPP");
    assert_eq!(message["text"], "karibu");
    assert_eq!(message["destinations"][0]["to"], "255712345678");
}

#[tokio::test]
async fn delivery_callback_updates_the_notification() {
    // Payments module off; the Notification model is built by default.
    let config: YekongaConfig =
        serde_json::from_value(json!({"authentication": {"secretToken": "s"}})).unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );

    // A submitted notification carrying the provider's message id.
    let note = app
        .query("Notification")
        .unwrap()
        .create(json!({
            "type": "sms",
            "recipient": "255712345678",
            "status": "submitted",
            "responseReference": "msg-42",
        }))
        .await
        .unwrap();
    let id = note["id"].as_str().unwrap().to_string();

    // Infobip posts a delivery report for that message id.
    let body = json!({"results": [
        {"messageId": "msg-42", "status": {"groupName": "DELIVERED"}, "seenAt": "2026-01-01T00:00:00Z"}
    ]});
    let request = HttpRequest::post("/infobip/sms/notification")
        .header("content-type", "application/json")
        .header("host", "localhost")
        .body(Body::from(body.to_string()))
        .unwrap();
    let response = app.router().oneshot(request).await.unwrap();
    assert_eq!(response.status().as_u16(), 200);
    let _ = response.into_body().collect().await.unwrap();

    let updated = app
        .query("Notification")
        .unwrap()
        .where_("id", id)
        .find_one()
        .await
        .unwrap()
        .unwrap();
    assert_eq!(updated["status"], "delivered");
    assert_eq!(updated["isSeen"], true);
}

/// A mock Infobip WhatsApp server: records the request and replies with a
/// pending message.
async fn mock_whatsapp() -> (SocketAddr, Arc<Mutex<Captured>>) {
    let captured = Arc::new(Mutex::new(Captured::default()));
    let state = captured.clone();

    async fn handler(
        State(captured): State<Arc<Mutex<Captured>>>,
        axum::extract::Path(message_type): axum::extract::Path<String>,
        headers: HeaderMap,
        body: String,
    ) -> ([(&'static str, &'static str); 1], String) {
        let mut slot = captured.lock().unwrap();
        slot.authorization = headers
            .get("authorization")
            .and_then(|v| v.to_str().ok())
            .unwrap_or_default()
            .to_string();
        slot.message_type = message_type;
        slot.body = serde_json::from_str(&body).unwrap_or(Value::Null);
        (
            [("content-type", "application/json")],
            json!({"messages": [{"messageId": "wa-1", "status": {"description": "Message accepted"}}]})
                .to_string(),
        )
    }

    let app = Router::new()
        .route("/whatsapp/1/message/{type}", post(handler))
        .with_state(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (addr, captured)
}

#[tokio::test]
async fn infobip_sends_a_whatsapp_message() {
    let (addr, captured) = mock_whatsapp().await;
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "apiGateway": {"whatsapp": {
            "provider": "infobip",
            "baseURL": format!("http://{addr}"),
            "sender": "44770",
            "apiKey": "wakey",
        }}
    }))
    .unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );

    let response = app.send_whatsapp_builtin("255712345678", "habari").await;
    assert_eq!(response.status, "SUCCESS", "{}", response.message);
    assert_eq!(response.message_id, "wa-1");

    let sent = captured.lock().unwrap();
    assert_eq!(sent.authorization, "App wakey");
    assert_eq!(sent.message_type, "text");
    // A text message posts the message object directly (no `messages` wrapper).
    assert_eq!(sent.body["from"], "44770");
    assert_eq!(sent.body["to"], "255712345678");
    assert_eq!(sent.body["content"]["text"], "habari");
}

fn whatsapp_app(addr: SocketAddr) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "apiGateway": {"whatsapp": {
            "provider": "infobip",
            "baseURL": format!("http://{addr}"),
            "sender": "44770",
            "apiKey": "wakey",
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
async fn infobip_sends_a_template_message() {
    let (addr, captured) = mock_whatsapp().await;
    let app = whatsapp_app(addr);

    let content = yekonga::gateway::WhatsappContent {
        message_type: "template".into(),
        template: "order_shipped".into(),
        placeholders: vec!["Ally".into(), "TZ123".into()],
        ..Default::default()
    };
    let response = app.send_whatsapp_content("255712345678", content).await;
    assert_eq!(response.status, "SUCCESS", "{}", response.message);

    let sent = captured.lock().unwrap();
    assert_eq!(sent.message_type, "template");
    // Non-text types are wrapped in a `messages` array.
    let message = &sent.body["messages"][0];
    assert_eq!(message["to"], "255712345678");
    assert_eq!(message["content"]["templateName"], "order_shipped");
    assert_eq!(
        message["content"]["templateData"]["body"]["placeholders"],
        json!(["Ally", "TZ123"])
    );
}

#[tokio::test]
async fn infobip_sends_a_media_message() {
    let (addr, captured) = mock_whatsapp().await;
    let app = whatsapp_app(addr);

    let content = yekonga::gateway::WhatsappContent {
        message_type: "image".into(),
        media_url: "https://cdn.example.tz/receipt.png".into(),
        caption: "Your receipt".into(),
        ..Default::default()
    };
    let response = app.send_whatsapp_content("255712345678", content).await;
    assert_eq!(response.status, "SUCCESS", "{}", response.message);

    let sent = captured.lock().unwrap();
    assert_eq!(sent.message_type, "image");
    let message = &sent.body["messages"][0];
    assert_eq!(
        message["content"]["mediaUrl"],
        "https://cdn.example.tz/receipt.png"
    );
    assert_eq!(message["content"]["caption"], "Your receipt");
}

/// A minimal SMTP server that accepts one message and returns its DATA
/// payload. Speaks just enough of the protocol for lettre's plain transport.
async fn mock_smtp() -> (SocketAddr, tokio::sync::oneshot::Receiver<String>) {
    use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let (tx, rx) = tokio::sync::oneshot::channel();

    tokio::spawn(async move {
        let (socket, _) = listener.accept().await.unwrap();
        let (read, mut write) = socket.into_split();
        let mut reader = BufReader::new(read);
        let mut line = String::new();

        write.write_all(b"220 mock ESMTP\r\n").await.unwrap();
        let mut data = String::new();
        loop {
            line.clear();
            if reader.read_line(&mut line).await.unwrap() == 0 {
                break;
            }
            let command = line.trim_end().to_uppercase();
            if command.starts_with("EHLO") || command.starts_with("HELO") {
                write.write_all(b"250 mock\r\n").await.unwrap();
            } else if command.starts_with("MAIL") || command.starts_with("RCPT") {
                write.write_all(b"250 OK\r\n").await.unwrap();
            } else if command == "DATA" {
                write
                    .write_all(b"354 End data with <CR><LF>.<CR><LF>\r\n")
                    .await
                    .unwrap();
                loop {
                    line.clear();
                    if reader.read_line(&mut line).await.unwrap() == 0 {
                        break;
                    }
                    if line.trim_end() == "." {
                        break;
                    }
                    data.push_str(&line);
                }
                write.write_all(b"250 OK: queued\r\n").await.unwrap();
            } else if command == "QUIT" {
                write.write_all(b"221 Bye\r\n").await.unwrap();
                break;
            } else {
                write.write_all(b"250 OK\r\n").await.unwrap();
            }
        }
        let _ = tx.send(data);
    });

    (addr, rx)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn smtp_sends_an_email() {
    let (addr, rx) = mock_smtp().await;
    let config: YekongaConfig = serde_json::from_value(json!({
        "authentication": {"secretToken": "s"},
        "mail": {"smtp": {
            "host": addr.ip().to_string(),
            "port": addr.port(),
            "secure": false,
            "from": "no-reply@shop.tz",
        }}
    }))
    .unwrap();
    let app = Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    );

    let response = app
        .send_email_builtin("user@example.com", "Welcome", "<b>Hello</b>")
        .await;
    assert_eq!(response.status, "SUCCESS", "{}", response.message);

    let data = rx.await.unwrap();
    assert!(data.contains("Subject: Welcome"), "{data}");
    assert!(data.contains("<b>Hello</b>"), "{data}");
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
