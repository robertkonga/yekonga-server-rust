//! Outbound SMS gateway providers (port of `gateway/` and
//! `cloud_send_functions.go`).
//!
//! Only the Beem SMS provider is ported. It is the built-in `send_sms` used by
//! the notification dispatch when no [`set_send_sms`](Yekonga::set_send_sms)
//! hook is registered and `apiGateway.sms` is configured. The Infobip SMS and
//! WhatsApp providers and the SMTP mail sender are not ported — register a
//! send function for those channels.

use base64::Engine;
use serde_json::{json, Value};

use crate::app::Yekonga;
use crate::config::SmsGatewayConfig;
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
