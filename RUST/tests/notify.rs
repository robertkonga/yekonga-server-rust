//! Notifications: queuing per channel, the send-function dispatch, and OTP
//! codes falling back to the notification queue.

use std::sync::{Arc, Mutex};

use serde_json::{json, Value};
use yekonga::db::DataMap;
use yekonga::notify::{NotificationParams, NotifiedUser};
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn app(config: Value) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

#[tokio::test]
async fn notify_queues_one_record_per_channel() {
    let app = app(json!({"authentication": {"secretToken": "s"}}));

    app.notify(
        &NotifiedUser {
            user_id: "u1".into(),
            email: "a@b.tz".into(),
            phone: "0712345678".into(),
            whatsapp: "0713333333".into(),
        },
        &NotificationParams {
            title: "Hi".into(),
            html: "<b>{{ notificationId }}</b>".into(),
            text: "text msg".into(),
            whatsapp: "wa msg".into(),
            ..Default::default()
        },
    )
    .await;

    let notes = app.query("Notification").unwrap().find().await.unwrap();
    assert_eq!(notes.len(), 3);

    let by_type = |t: &str| notes.iter().find(|n| n["type"] == t).unwrap().clone();
    let mail = by_type("mail");
    assert_eq!(mail["recipient"], "a@b.tz");
    assert_eq!(mail["status"], "waiting");
    // The HTML body is templated with the notification's own id.
    assert_eq!(
        mail["content"],
        format!("<b>{}</b>", mail["id"].as_str().unwrap())
    );

    assert_eq!(by_type("sms")["recipient"], "255712345678");
    assert_eq!(by_type("sms")["content"], "text msg");
    assert_eq!(by_type("whatsapp")["recipient"], "255713333333");
}

#[tokio::test]
async fn notify_skips_empty_or_invalid_channels() {
    let app = app(json!({"authentication": {"secretToken": "s"}}));
    app.notify(
        &NotifiedUser {
            user_id: "u1".into(),
            email: "not-an-email".into(),
            phone: "123".into(),
            ..Default::default()
        },
        &NotificationParams {
            html: "body".into(),
            text: "sms".into(),
            ..Default::default()
        },
    )
    .await;
    assert_eq!(app.query("Notification").unwrap().count().await.unwrap(), 0);
}

#[tokio::test]
async fn send_functions_receive_queued_notifications() {
    let app = app(json!({"authentication": {"secretToken": "s"}}));
    let sent: Arc<Mutex<Vec<DataMap>>> = Arc::new(Mutex::new(Vec::new()));

    let sms_out = sent.clone();
    app.set_send_sms(move |note: DataMap| {
        let sms_out = sms_out.clone();
        let mut guard = sms_out.lock().unwrap();
        guard.push(note);
        drop(guard);
        Box::pin(async move {})
    });

    app.notify(
        &NotifiedUser {
            user_id: "u1".into(),
            phone: "0712345678".into(),
            ..Default::default()
        },
        &NotificationParams {
            text: "hello".into(),
            ..Default::default()
        },
    )
    .await;

    // Deliver the queue directly (the cron job calls the same path).
    app.deliver_notifications_now().await;

    {
        let delivered = sent.lock().unwrap();
        assert_eq!(delivered.len(), 1);
        assert_eq!(delivered[0]["content"], "hello");
    }

    // The record is marked submitted.
    let note = app
        .query("Notification")
        .unwrap()
        .find_one()
        .await
        .unwrap()
        .unwrap();
    assert_eq!(note["status"], "submitted");
}

#[tokio::test]
async fn otp_codes_fall_back_to_the_notification_queue() {
    // With no set_otp_sender hook, an OTP request queues a notification.
    let app = app(json!({
        "isAuthorizationServer": true,
        "graphql": {"apiRoute": "/graphql", "apiAuthRoute": "/auth"},
        "authentication": {"secretToken": "s"}
    }));

    let request = http::Request::post("/auth")
        .header("content-type", "application/json")
        .header("host", "shop.tz")
        .body(axum::body::Body::from(
            json!({"query": r#"mutation { otp(input: {username: "a@b.tz"}) { status } }"#})
                .to_string(),
        ))
        .unwrap();
    tower::ServiceExt::oneshot(app.router(), request)
        .await
        .unwrap();

    let note = app
        .query("Notification")
        .unwrap()
        .where_("type", "mail")
        .find_one()
        .await
        .unwrap()
        .expect("an OTP email was queued");
    assert_eq!(note["recipient"], "a@b.tz");
    assert_eq!(note["title"], "OTP");
    // The queued content carries the OTP verification code.
    let stored = app
        .query("UserVerification")
        .unwrap()
        .skip_before_commit()
        .find_one()
        .await
        .unwrap()
        .unwrap();
    let code = stored["otpCode"].as_str().unwrap();
    assert!(note["content"].as_str().unwrap().starts_with(code));
}
