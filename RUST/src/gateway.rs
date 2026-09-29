//! Outbound SMS gateway providers (port of `gateway/` and
//! `cloud_send_functions.go`).
//!
//! Ported: the Beem SMS provider, the Infobip WhatsApp provider (text,
//! template, media and raw-content messages) and the SMTP mail sender. Each is
//! the built-in used by the notification dispatch when the matching
//! `set_send_*` hook isn't registered and the channel is configured. The
//! Infobip SMS provider isn't ported — register a send function for it.

use base64::Engine;
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};

use crate::app::Yekonga;
use crate::config::{SmsGatewayConfig, WhatsappGatewayConfig};
use crate::db::values::new_object_id;
use crate::helper::random_digits;

/// The result of a send attempt (a subset of Go's `setting.SendResponse`).
#[derive(Clone, Debug, Default, PartialEq)]
pub struct SendResponse {
    pub status: String,
    pub message: String,
    pub message_id: String,
    pub code: i64,
}

impl Yekonga {
    /// Sends an SMS through the configured provider (Go's `SendSMS` built-in
    /// path). Only Beem is supported; other providers return a failure.
    pub async fn send_sms_builtin(&self, phone: &str, text: &str) -> SendResponse {
        let config = &self.config().api_gateway.sms;
        match config.provider.as_str() {
            "" | "beem" => beem_send(config, phone, text).await,
            other => SendResponse {
                status: "FAILED".into(),
                message: format!("SMS provider {other:?} is not supported by the Rust port"),
                ..Default::default()
            },
        }
    }

    /// Sends an HTML email over SMTP (Go's `SendEmail` built-in path), using
    /// `mail.smtp` (host, port, TLS, credentials, from address).
    pub async fn send_email_builtin(&self, to: &str, subject: &str, html: &str) -> SendResponse {
        smtp_send(&self.config().mail.smtp, to, subject, html).await
    }

    /// Sends a WhatsApp text message through the configured provider (Go's
    /// `SendWhatsapp` built-in path). Only Infobip is supported.
    pub async fn send_whatsapp_builtin(&self, phone: &str, text: &str) -> SendResponse {
        self.send_whatsapp_content(
            phone,
            WhatsappContent {
                text: text.into(),
                ..Default::default()
            },
        )
        .await
    }

    /// Sends a WhatsApp message of any type (text, template, media, or a raw
    /// `content` body) through the configured provider — the full Go
    /// `SendWhatsapp` content path. Only Infobip is supported.
    pub async fn send_whatsapp_content(
        &self,
        phone: &str,
        content: WhatsappContent,
    ) -> SendResponse {
        let config = &self.config().api_gateway.whatsapp;
        match config.provider.as_str() {
            "" | "infobip" => infobip_whatsapp_send(config, phone, &content).await,
            other => SendResponse {
                status: "FAILED".into(),
                message: format!("WhatsApp provider {other:?} is not supported by the Rust port"),
                ..Default::default()
            },
        }
    }
}

/// Sends one HTML email via SMTP (port of `helper/mail.go`'s `sendSMTP`).
/// With `secure`, connects over TLS; otherwise a plain connection with
/// STARTTLS when the server offers it. Credentials are used when a username
/// is set.
async fn smtp_send(
    config: &crate::config::SmtpConfig,
    to: &str,
    subject: &str,
    html: &str,
) -> SendResponse {
    use lettre::transport::smtp::authentication::Credentials;
    use lettre::transport::smtp::AsyncSmtpTransport;
    use lettre::{AsyncTransport, Message, Tokio1Executor};

    let failed = |message: String| SendResponse {
        status: "FAILED".into(),
        message,
        ..Default::default()
    };
    if config.host.is_empty() || config.from.is_empty() {
        return failed("SMTP is not configured".into());
    }

    let email = match Message::builder()
        .from(match config.from.parse() {
            Ok(from) => from,
            Err(e) => return failed(format!("invalid from address: {e}")),
        })
        .to(match to.parse() {
            Ok(to) => to,
            Err(e) => return failed(format!("invalid recipient: {e}")),
        })
        .subject(subject)
        .header(lettre::message::header::ContentType::TEXT_HTML)
        .body(html.to_string())
    {
        Ok(email) => email,
        Err(e) => return failed(e.to_string()),
    };

    let mut builder = if config.secure {
        match AsyncSmtpTransport::<Tokio1Executor>::relay(&config.host) {
            Ok(builder) => builder,
            Err(e) => return failed(e.to_string()),
        }
    } else {
        AsyncSmtpTransport::<Tokio1Executor>::builder_dangerous(&config.host)
    };
    if config.port > 0 {
        builder = builder.port(config.port as u16);
    }
    if !config.username.is_empty() {
        builder = builder.credentials(Credentials::new(
            config.username.clone(),
            config.password.clone(),
        ));
    }

    match builder.build().send(email).await {
        Ok(_) => SendResponse {
            status: "SUCCESS".into(),
            message: "sent".into(),
            ..Default::default()
        },
        Err(e) => failed(e.to_string()),
    }
}

/// `base` as a full URL: kept as-is when it already has a scheme, otherwise
/// `https://` (so a mock can be reached over `http://`).
fn with_scheme(base: &str) -> String {
    let base = base.trim_end_matches('/');
    if base.starts_with("http://") || base.starts_with("https://") {
        base.to_string()
    } else {
        format!("https://{base}")
    }
}

/// A WhatsApp message body (Go's `setting.Content`). Deserializes from the
/// notification `content` JSON, and is built by callers for template/media
/// messages. `content` is a raw provider body used as-is.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct WhatsappContent {
    #[serde(rename = "type", default, skip_serializing_if = "String::is_empty")]
    pub message_type: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub template: String,
    #[serde(
        rename = "templateName",
        default,
        skip_serializing_if = "String::is_empty"
    )]
    pub template_name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub placeholders: Vec<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(rename = "mediaUrl", default, skip_serializing_if = "String::is_empty")]
    pub media_url: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub caption: String,
    #[serde(default, skip_serializing_if = "Map::is_empty")]
    pub content: Map<String, Value>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub language: String,
}

/// The Infobip message type and `content` body for a [`WhatsappContent`],
/// following Go's `InfobipProvider.Send` branching: an explicit `type` wins,
/// otherwise a raw `content` map makes it a template; a `templateName` sends
/// the whole content object, a raw map is sent as-is, a template builds the
/// `templateData` body, text falls back to `"..."`, and a `mediaUrl` sends
/// `{mediaUrl, caption}`.
fn whatsapp_message(content: &WhatsappContent) -> (String, Value) {
    let message_type = if !content.message_type.is_empty() {
        content.message_type.clone()
    } else if !content.content.is_empty() {
        "template".into()
    } else {
        "text".into()
    };

    let body = if !content.template_name.is_empty() {
        serde_json::to_value(content).unwrap_or(Value::Null)
    } else if !content.content.is_empty() {
        Value::Object(content.content.clone())
    } else if message_type == "template" {
        let template = if content.template.is_empty() {
            &content.template_name
        } else {
            &content.template
        };
        json!({
            "templateName": template,
            "templateData": {"body": {"placeholders": content.placeholders}},
            "language": "en",
        })
    } else if message_type == "text" {
        let mut text = content.text.clone();
        if text.is_empty() {
            text = content
                .content
                .get("text")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .into();
        }
        if text.is_empty() {
            text = "...".into();
        }
        json!({ "text": text })
    } else if !content.media_url.is_empty() {
        json!({"mediaUrl": content.media_url, "caption": content.caption})
    } else {
        Value::Null
    };

    (message_type, body)
}

/// Sends one WhatsApp message via Infobip's `/whatsapp/1/message/:type` (port
/// of `gateway/whatsapp/infobip.go`): `App` API-key auth, a text message posts
/// the message object directly while other types wrap it in `messages: [...]`,
/// and a non-empty `messages` array in the response means success.
async fn infobip_whatsapp_send(
    config: &WhatsappGatewayConfig,
    phone: &str,
    content: &WhatsappContent,
) -> SendResponse {
    if config.base_url.is_empty() || config.api_key.is_empty() {
        return SendResponse {
            status: "FAILED".into(),
            message: "WhatsApp gateway is not configured".into(),
            ..Default::default()
        };
    }
    let message_id = new_object_id();
    let (message_type, content_body) = whatsapp_message(content);
    let single = json!({
        "from": config.sender,
        "to": phone,
        "messageId": message_id,
        "content": content_body,
        "callbackData": format!("{{\"id\": \"{message_id}\"}}"),
    });
    // Text posts the message object directly; every other type is wrapped.
    let body = if message_type == "text" {
        single
    } else {
        json!({ "messages": [single] })
    };

    let request = reqwest::Client::new()
        .post(format!(
            "{}/whatsapp/1/message/{message_type}",
            with_scheme(&config.base_url)
        ))
        .header("accept", "application/json")
        .header("authorization", format!("App {}", config.api_key))
        .json(&body)
        .send()
        .await;

    let response = match request {
        Ok(response) => response,
        Err(err) => {
            return SendResponse {
                status: "FAILED".into(),
                message: err.to_string(),
                ..Default::default()
            }
        }
    };
    let data: Value = match response.json().await {
        Ok(data) => data,
        Err(_) => {
            return SendResponse {
                status: "FAILED".into(),
                message: "Failed to parse response".into(),
                ..Default::default()
            }
        }
    };

    match data.get("messages").and_then(Value::as_array) {
        Some(messages) if !messages.is_empty() => SendResponse {
            status: "SUCCESS".into(),
            message: messages[0]["status"]["description"]
                .as_str()
                .unwrap_or_default()
                .to_string(),
            message_id: messages[0]["messageId"]
                .as_str()
                .unwrap_or(&message_id)
                .to_string(),
            code: 0,
        },
        _ => SendResponse {
            status: "FAILED".into(),
            message: data["requestError"]["serviceException"]["text"]
                .as_str()
                .unwrap_or("send failed")
                .to_string(),
            ..Default::default()
        },
    }
}

/// The Beem base URL: `apiGateway.sms.baseURL` if set, else Beem's own host.
fn beem_base_url(config: &SmsGatewayConfig) -> String {
    let base = if config.base_url.is_empty() {
        "https://apisms.beem.africa"
    } else {
        &config.base_url
    };
    base.trim_end_matches('/').to_string()
}

/// Sends one SMS via Beem's `/v1/send` (port of `gateway/sms/beem.go`):
/// HTTP Basic auth with `apiKey:secretKey`, and `code == 100` means success.
async fn beem_send(config: &SmsGatewayConfig, phone: &str, text: &str) -> SendResponse {
    let sender = if config.sender.is_empty() {
        "INFO"
    } else {
        &config.sender
    };
    let body = json!({
        "source_addr": sender,
        "schedule_time": "",
        "message": text,
        "encoding": 0,
        "recipients": [{"recipient_id": random_recipient_id(), "dest_addr": phone}],
    });
    let auth = base64::engine::general_purpose::STANDARD
        .encode(format!("{}:{}", config.api_key, config.secret_key));

    let request = reqwest::Client::new()
        .post(format!("{}/v1/send", beem_base_url(config)))
        .header("accept", "application/json")
        .header("authorization", format!("Basic {auth}"))
        .json(&body)
        .send()
        .await;

    let response = match request {
        Ok(response) => response,
        Err(err) => {
            return SendResponse {
                status: "FAILED".into(),
                message: err.to_string(),
                ..Default::default()
            }
        }
    };

    let data: Value = match response.json().await {
        Ok(data) => data,
        Err(_) => {
            return SendResponse {
                status: "FAILED".into(),
                message: "Failed to parse response".into(),
                ..Default::default()
            }
        }
    };

    let code = data.get("code").and_then(Value::as_i64).unwrap_or(0);
    let request_id = data
        .get("request_id")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string();
    SendResponse {
        status: if code == 100 { "SUCCESS" } else { "FAILED" }.into(),
        message: data
            .get("message")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string(),
        message_id: request_id,
        code,
    }
}

/// Beem's per-recipient id (Go uses a random int).
fn random_recipient_id() -> i64 {
    random_digits(6).parse().unwrap_or(1)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn beem_base_url_defaults_and_overrides() {
        let mut config = SmsGatewayConfig::default();
        assert_eq!(beem_base_url(&config), "https://apisms.beem.africa");
        config.base_url = "http://localhost:9000/".into();
        assert_eq!(beem_base_url(&config), "http://localhost:9000");
    }
}
