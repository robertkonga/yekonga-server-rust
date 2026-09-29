//! The Stripe payment provider (port of `gateway/payment/stripe.go`).
//!
//! Stripe Checkout Sessions (hosted checkout) + PaymentIntents. Credentials:
//! `secret_key` (`sk_...`) for the REST API, and `webhook_secret` (`whsec_...`)
//! for webhook signature verification. Stripe has no mobile-money push flow.

use hmac::{Hmac, Mac};
use serde_json::Value;
use sha2::Sha256;

use super::{
    PaymentRequest, PaymentResponse, PaymentResult, ProviderError, RefundRequest, RefundResult,
    WebhookError, WebhookEvent, STATUS_FAILED, STATUS_PENDING, STATUS_REFUNDED, STATUS_SUCCEEDED,
};
use crate::config::PaymentProviderConfig;

const BASE_URL: &str = "https://api.stripe.com/v1";
/// Webhook timestamps older than this are rejected as replays (Stripe's default).
const SIGNATURE_TOLERANCE_SECS: i64 = 300;

/// Currencies Stripe expects/returns without a minor unit (already in the base
/// unit), so amounts are not multiplied by 100.
const ZERO_DECIMAL: &[&str] = &[
    "BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA", "PYG", "RWF", "UGX", "VND", "VUV",
    "XAF", "XOF", "XPF",
];

pub(crate) struct Stripe {
    secret_key: String,
    webhook_secret: String,
    base: String,
}

impl Stripe {
    /// Builds the provider from its config, or `None` when `secret_key` is
    /// missing.
    pub(crate) fn new(config: &PaymentProviderConfig) -> Option<Self> {
        let secret_key = config.credentials.get("secret_key")?.trim().to_string();
        if secret_key.is_empty() {
            return None;
        }
        let base = if config.base_url.trim().is_empty() {
            BASE_URL.to_string()
        } else {
            config.base_url.trim_end_matches('/').to_string()
        };
        Some(Self {
            secret_key,
            webhook_secret: config
                .credentials
                .get("webhook_secret")
                .cloned()
                .unwrap_or_default(),
            base,
        })
    }

    fn error(&self, message: impl Into<String>) -> ProviderError {
        ProviderError {
            provider: "stripe".into(),
            code: String::new(),
            message: message.into(),
        }
    }

    /// A form POST/GET to the Stripe API, returning the parsed JSON body. A
    /// non-2xx response surfaces `error.message`/`error.code` from Stripe.
    async fn call(
        &self,
        method: reqwest::Method,
        path: &str,
        form: &[(String, String)],
    ) -> Result<Value, ProviderError> {
        let client = reqwest::Client::new();
        let url = format!("{}{path}", self.base);
        let request = if method == reqwest::Method::GET {
            client.get(&url).query(form)
        } else {
            client.request(method, &url).form(form)
        };

        let response = request
            .bearer_auth(&self.secret_key)
            .send()
            .await
            .map_err(|e| self.error(e.to_string()))?;
        let status = response.status();
        let body: Value = response
            .json()
            .await
            .map_err(|e| self.error(e.to_string()))?;

        if !status.is_success() {
            let error = &body["error"];
            let code = error["code"].as_str().or_else(|| error["type"].as_str());
            return Err(ProviderError {
                provider: "stripe".into(),
                code: code.unwrap_or_default().to_string(),
                message: error["message"]
                    .as_str()
                    .unwrap_or("stripe request failed")
                    .to_string(),
            });
        }
        Ok(body)
    }

    /// Opens a hosted Checkout Session and returns its URL.
    pub(crate) async fn create_payment(
        &self,
        req: &PaymentRequest,
    ) -> Result<PaymentResponse, ProviderError> {
        if req.return_url.is_empty() {
            return Err(self.error("stripe needs a return url"));
        }
        let cancel_url = if req.cancel_url.is_empty() {
            &req.return_url
        } else {
            &req.cancel_url
        };
        let name = if req.description.is_empty() {
            format!("Payment {}", req.reference)
        } else {
            req.description.clone()
        };

        let mut form: Vec<(String, String)> = vec![
            ("mode".into(), "payment".into()),
            ("success_url".into(), req.return_url.clone()),
            ("cancel_url".into(), cancel_url.clone()),
            ("client_reference_id".into(), req.reference.clone()),
            ("line_items[0][quantity]".into(), "1".into()),
            (
                "line_items[0][price_data][currency]".into(),
                req.currency.to_lowercase(),
            ),
            (
                "line_items[0][price_data][unit_amount]".into(),
                to_minor_units(req.amount, &req.currency).to_string(),
            ),
            (
                "line_items[0][price_data][product_data][name]".into(),
                truncate(&name, 250),
            ),
            (
                "payment_intent_data[metadata][reference]".into(),
                req.reference.clone(),
            ),
        ];
        if !req.customer.email.is_empty() {
            form.push(("customer_email".into(), req.customer.email.clone()));
        }
        for (key, value) in &req.metadata {
            let value = value
                .as_str()
                .map(String::from)
                .unwrap_or_else(|| value.to_string());
            form.push((format!("metadata[{key}]"), value));
        }

        let raw = self
            .call(reqwest::Method::POST, "/checkout/sessions", &form)
            .await?;
        let url = raw["url"].as_str().unwrap_or_default();
        if url.is_empty() {
            return Err(self.error("checkout url was not returned"));
        }
        Ok(PaymentResponse {
            provider: "stripe".into(),
            reference: req.reference.clone(),
            provider_ref: raw["id"].as_str().unwrap_or_default().to_string(),
            checkout_url: url.to_string(),
            status: STATUS_PENDING.into(),
            raw,
        })
    }

    /// Reads the checkout session by its id (the provider reference).
    pub(crate) async fn verify_payment(
        &self,
        provider_ref: &str,
    ) -> Result<PaymentResult, ProviderError> {
        if provider_ref.is_empty() {
            return Err(self.error("stripe looks payments up by checkout session id"));
        }
        let raw = self
            .call(
                reqwest::Method::GET,
                &format!("/checkout/sessions/{provider_ref}"),
                &[("expand[]".into(), "payment_intent".into())],
            )
            .await?;
        Ok(session_result(&raw))
    }

    /// Refunds a payment by its PaymentIntent id.
    pub(crate) async fn refund(&self, req: &RefundRequest) -> Result<RefundResult, ProviderError> {
        let mut form: Vec<(String, String)> =
            vec![("payment_intent".into(), req.provider_payment_id.clone())];
        if req.amount > 0.0 {
            form.push((
                "amount".into(),
                to_minor_units(req.amount, &req.currency).to_string(),
            ));
        }
        if !req.reason.is_empty() {
            form.push(("metadata[reason]".into(), truncate(&req.reason, 500)));
        }

        let raw = self.call(reqwest::Method::POST, "/refunds", &form).await?;
        let status = match raw["status"].as_str().unwrap_or_default() {
            "succeeded" => STATUS_REFUNDED,
            "failed" | "canceled" => STATUS_FAILED,
            _ => STATUS_PENDING,
        };
        Ok(RefundResult {
            provider: "stripe".into(),
            provider_refund_id: raw["id"].as_str().unwrap_or_default().to_string(),
            status: status.into(),
            raw,
        })
    }

    /// Verifies the `Stripe-Signature` header and decodes the event (Go's
    /// `ParseWebhook`). The signature is an HMAC-SHA256 over `"{timestamp}.{body}"`
    /// keyed on the endpoint's `whsec_...` secret, compared against a `v1` entry,
    /// and timestamps older than 5 minutes are rejected.
    pub(crate) fn parse_webhook(
        &self,
        signature: &str,
        body: &[u8],
    ) -> Result<WebhookEvent, WebhookError> {
        if self.webhook_secret.is_empty() {
            return Err(WebhookError::InvalidRequest);
        }
        verify_signature(signature, body, &self.webhook_secret)?;

        let event: Value =
            serde_json::from_slice(body).map_err(|_| WebhookError::InvalidWebhook)?;
        let event_type = event["type"].as_str().unwrap_or_default().to_string();
        let object = &event["data"]["object"];

        let mut webhook = WebhookEvent {
            provider: "stripe".into(),
            event_type: event_type.clone(),
            result: None,
            ack: None,
        };
        match event_type.as_str() {
            "checkout.session.completed" | "checkout.session.expired" => {
                webhook.result = Some(session_result(object));
            }
            "charge.refunded" if object["refunded"].as_bool().unwrap_or(false) => {
                let currency = object["currency"].as_str().unwrap_or_default();
                webhook.result = Some(PaymentResult {
                    provider_payment_id: object["payment_intent"]
                        .as_str()
                        .unwrap_or_default()
                        .into(),
                    status: STATUS_REFUNDED.into(),
                    amount: from_minor_units(
                        object["amount_refunded"].as_i64().unwrap_or(0),
                        currency,
                    ),
                    currency: currency.to_uppercase(),
                    raw: object.clone(),
                    ..Default::default()
                });
            }
            _ => {}
        }
        Ok(webhook)
    }
}

/// Maps a Checkout Session object to a [`PaymentResult`].
fn session_result(session: &Value) -> PaymentResult {
    let payment_status = session["payment_status"].as_str().unwrap_or_default();
    let session_status = session["status"].as_str().unwrap_or_default();
    let currency = session["currency"].as_str().unwrap_or_default();

    let (status, failure_reason) = match () {
        _ if payment_status == "paid" || payment_status == "no_payment_required" => {
            (STATUS_SUCCEEDED, String::new())
        }
        _ if session_status == "expired" => (STATUS_FAILED, "expired".to_string()),
        _ => (STATUS_PENDING, String::new()),
    };

    PaymentResult {
        reference: session["client_reference_id"]
            .as_str()
            .unwrap_or_default()
            .into(),
        provider_ref: session["id"].as_str().unwrap_or_default().into(),
        provider_payment_id: payment_intent_id(&session["payment_intent"]),
        status: status.into(),
        amount: from_minor_units(session["amount_total"].as_i64().unwrap_or(0), currency),
        currency: currency.to_uppercase(),
        method: "card".into(),
        failure_reason,
        raw: session.clone(),
    }
}

/// The PaymentIntent id, whether the field is the string id or an expanded object.
fn payment_intent_id(value: &Value) -> String {
    match value {
        Value::String(id) => id.clone(),
        Value::Object(_) => value["id"].as_str().unwrap_or_default().to_string(),
        _ => String::new(),
    }
}

/// Whether `currency` is one Stripe treats as having no minor unit.
fn is_zero_decimal(currency: &str) -> bool {
    let upper = currency.to_uppercase();
    ZERO_DECIMAL.contains(&upper.as_str())
}

/// A major-unit amount as Stripe's integer minor units (cents), rounding.
fn to_minor_units(amount: f64, currency: &str) -> i64 {
    let factor = if is_zero_decimal(currency) {
        1.0
    } else {
        100.0
    };
    (amount * factor + 0.5).floor() as i64
}

/// Stripe's integer minor units back to a major-unit amount.
fn from_minor_units(amount: i64, currency: &str) -> f64 {
    let factor = if is_zero_decimal(currency) {
        1.0
    } else {
        100.0
    };
    amount as f64 / factor
}

/// Truncates `s` to at most `max` characters (Stripe field limits).
fn truncate(s: &str, max: usize) -> String {
    s.chars().take(max).collect()
}

/// Verifies a `t=...,v1=...` Stripe-Signature header against the body.
fn verify_signature(header: &str, body: &[u8], secret: &str) -> Result<(), WebhookError> {
    if header.is_empty() {
        return Err(WebhookError::InvalidWebhook);
    }
    let mut timestamp = None;
    let mut signatures = Vec::new();
    for part in header.split(',') {
        match part.split_once('=') {
            Some(("t", value)) => timestamp = Some(value),
            Some(("v1", value)) => signatures.push(value),
            _ => {}
        }
    }
    let (Some(timestamp), false) = (timestamp, signatures.is_empty()) else {
        return Err(WebhookError::InvalidWebhook);
    };
    let ts: i64 = timestamp
        .parse()
        .map_err(|_| WebhookError::InvalidWebhook)?;
    if (now_unix() - ts).abs() > SIGNATURE_TOLERANCE_SECS {
        return Err(WebhookError::InvalidWebhook);
    }

    let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes())
        .map_err(|_| WebhookError::InvalidWebhook)?;
    mac.update(timestamp.as_bytes());
    mac.update(b".");
    mac.update(body);
    let want = hex::encode(mac.finalize().into_bytes());

    // Constant-time compare against each provided v1 signature.
    if signatures.iter().any(|got| {
        got.len() == want.len()
            && got
                .bytes()
                .zip(want.bytes())
                .fold(0u8, |acc, (a, b)| acc | (a ^ b))
                == 0
    }) {
        Ok(())
    } else {
        Err(WebhookError::InvalidWebhook)
    }
}

fn now_unix() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}
