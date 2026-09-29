//! Notifications and the send-function dispatch (ports of `yekonga/notify.go`,
//! `notification.go` and `cloud_send_functions.go`).
//!
//! [`Yekonga::notify`] queues `Notification` records (one per channel: mail,
//! sms, whatsapp) with status `waiting`. A cron job registered at startup picks
//! up waiting records every 10 seconds, hands each to the matching send
//! function, and marks it `submitted`.
//!
//! The SMS, WhatsApp and mail *providers* (Beem, SMTP, …) aren't ported;
//! register a sender with [`Yekonga::set_send_sms`], [`set_send_email`] or
//! [`set_send_whatsapp`] to deliver, otherwise a queued notification is logged
//! and marked submitted without being sent.

use std::sync::Arc;
use std::time::Duration;

use serde_json::{json, Value};

use crate::app::{BoxFuture, Yekonga};
use crate::db::values::new_object_id;
use crate::db::DataMap;
use crate::helper::{format_phone, is_email, is_phone};

/// Who a notification is for (Go's `NotifiedUser`).
#[derive(Clone, Debug, Default)]
pub struct NotifiedUser {
    pub user_id: String,
    pub email: String,
    pub phone: String,
    pub whatsapp: String,
}

/// The content of a notification (a subset of Go's `NotificationParams`).
#[derive(Clone, Debug, Default)]
pub struct NotificationParams {
    pub title: String,
    /// The email body (HTML).
    pub html: String,
    /// The SMS text.
    pub text: String,
    /// The WhatsApp message.
    pub whatsapp: String,
    pub profile_id: Option<String>,
    pub reference_id: Option<String>,
    pub reference_name: Option<String>,
    pub sender_name: Option<String>,
    pub reply_to: Option<String>,
    pub name: Option<String>,
    pub link: Option<String>,
    pub attachment: Option<String>,
}

/// A queued notification handed to a send function.
pub type SendFn = Arc<dyn Fn(DataMap) -> BoxFuture<()> + Send + Sync>;

/// The registered send functions, one per channel.
#[derive(Default, Clone)]
pub(crate) struct SendFunctions {
    pub sms: Option<SendFn>,
    pub email: Option<SendFn>,
    pub whatsapp: Option<SendFn>,
}

impl Yekonga {
    /// Registers the function that delivers SMS notifications.
    pub fn set_send_sms(
        &self,
        function: impl Fn(DataMap) -> BoxFuture<()> + Send + Sync + 'static,
    ) {
        self.send_functions()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .sms = Some(Arc::new(function));
    }

    /// Registers the function that delivers email notifications.
    pub fn set_send_email(
        &self,
        function: impl Fn(DataMap) -> BoxFuture<()> + Send + Sync + 'static,
    ) {
        self.send_functions()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .email = Some(Arc::new(function));
    }

    /// Registers the function that delivers WhatsApp notifications.
    pub fn set_send_whatsapp(
        &self,
        function: impl Fn(DataMap) -> BoxFuture<()> + Send + Sync + 'static,
    ) {
        self.send_functions()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .whatsapp = Some(Arc::new(function));
    }

    /// Queues notifications for a user: an email when `html` is set and the
    /// user has an email address, an SMS when `text` is set and a valid phone
    /// number, and a WhatsApp message likewise (Go's `Notify`).
    pub async fn notify(&self, user: &NotifiedUser, params: &NotificationParams) {
        let Ok(query) = self.query("Notification") else {
            return;
        };
        let now = crate::db::values::now_string();

        let base = json!({
            "profileId": params.profile_id,
            "userId": user.user_id,
            "referenceId": params.reference_id,
            "referenceName": params.reference_name,
            "recipientName": params.name,
            "replyTo": params.reply_to,
            "title": params.title,
            "link": params.link,
            "attachment": params.attachment,
            "senderName": params.sender_name,
            "isSeen": false,
            "status": "waiting",
            "timestamp": now,
        });

        // Email.
        if !params.html.is_empty() && is_email(&user.email) {
            let mut note = base.clone();
            let id = new_object_id();
            note["id"] = json!(id);
            note["recipient"] = json!(user.email);
            note["type"] = json!("mail");
            note["content"] = json!(crate::helper::text_template(
                &params.html,
                &json!({"notificationId": id}),
            ));
            let _ = query.clone().create(note).await;
        }

        // SMS.
        if !params.text.is_empty() {
            let phone = format_phone(&user.phone);
            if is_phone(&phone) {
                let mut note = base.clone();
                note["recipient"] = json!(phone);
                note["type"] = json!("sms");
                note["content"] = json!(params.text);
                let _ = query.clone().create(note).await;
            }
        }

        // WhatsApp.
        if !params.whatsapp.is_empty() {
            let whatsapp = format_phone(&user.whatsapp);
            if is_phone(&whatsapp) {
                let mut note = base.clone();
                note["recipient"] = json!(whatsapp);
                note["type"] = json!("whatsapp");
                note["content"] = json!(params.whatsapp);
                let _ = query.clone().create(note).await;
            }
        }
    }

    /// Registers the cron job that delivers queued notifications (Go's
    /// `setNotification`). It runs only when `hasCronjob` is set and the
    /// server is started.
    pub(crate) fn register_notification_job(&self) {
        self.register_cronjob(
            "SystemNotification",
            Duration::from_secs(10),
            |app, _time| Box::pin(async move { app.process_notifications().await }),
        );
    }

    /// Delivers every queued notification now, instead of waiting for the
    /// cron job (useful for tests and manual flushes).
    pub async fn deliver_notifications_now(&self) {
        self.process_notifications().await;
    }

    /// Delivers every `waiting` notification and marks it `submitted`.
    async fn process_notifications(&self) {
        let Ok(query) = self.query("Notification") else {
            return;
        };
        let waiting = query
            .clone()
            .where_("status", json!({"equalTo": "waiting"}))
            .find()
            .await
            .unwrap_or_default();

        for note in waiting {
            let id = crate::auth::string_of(note.get("_id"));
            let message_id = match crate::auth::string_of(note.get("type")).as_str() {
                "sms" => self.dispatch(|f| &f.sms, note, "SMS").await,
                "mail" => self.dispatch(|f| &f.email, note, "email").await,
                "whatsapp" => self.dispatch(|f| &f.whatsapp, note, "WhatsApp").await,
                _ => None,
            };
            // Record the provider's message id so a delivery-status callback can
            // find this notification (`responseReference`), as Go stores it.
            let mut update = json!({"status": "submitted"});
            if let Some(message_id) = message_id.filter(|m| !m.is_empty()) {
                update["responseReference"] = Value::String(message_id);
            }
            let _ = query.clone().where_("_id", id).update(update).await;
        }
    }

    /// Hands one notification to its channel's send function, or logs that no
    /// sender is registered. Returns the provider's message id from a built-in
    /// send (so the caller can store it for the delivery-status callback);
    /// `None` for a registered hook, a failure, or no sender.
    async fn dispatch(
        &self,
        pick: impl Fn(&SendFunctions) -> &Option<SendFn>,
        note: DataMap,
        channel: &str,
    ) -> Option<String> {
        let function = pick(
            &self
                .send_functions()
                .read()
                .unwrap_or_else(|e| e.into_inner()),
        )
        .clone();
        if let Some(function) = function {
            function(note).await;
            return None;
        }

        // No registered hook: use a built-in provider where one exists.
        let recipient = crate::auth::string_of(note.get("recipient"));
        let content = crate::auth::string_of(note.get("content"));
        let result = if channel == "email" && !self.config().mail.smtp.host.is_empty() {
            let subject = crate::auth::string_of(note.get("title"));
            self.send_email_builtin(&recipient, &subject, &content)
                .await
        } else if channel == "SMS" && !self.config().api_gateway.sms.api_key.is_empty() {
            self.send_sms_builtin(&recipient, &content).await
        } else if channel == "WhatsApp" && !self.config().api_gateway.whatsapp.api_key.is_empty() {
            // A content that parses as a JSON object is sent as a structured
            // (template/media) message with that object as the body, as Go's
            // dispatch does; anything else is a plain text message.
            match serde_json::from_str::<serde_json::Map<String, serde_json::Value>>(&content) {
                Ok(map) if !map.is_empty() => {
                    self.send_whatsapp_content(
                        &recipient,
                        crate::gateway::WhatsappContent {
                            content: map,
                            ..Default::default()
                        },
                    )
                    .await
                }
                _ => self.send_whatsapp_builtin(&recipient, &content).await,
            }
        } else {
            tracing::warn!(
                channel,
                recipient,
                "notification not sent: no {channel} sender is registered"
            );
            return None;
        };

        if result.status != "SUCCESS" {
            tracing::warn!(channel, recipient, message = %result.message, "{channel} send failed");
            return None;
        }
        Some(result.message_id)
    }
}
