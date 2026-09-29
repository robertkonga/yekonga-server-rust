//! The Selcom payment provider (port of `gateway/payment/selcom.go`).
//!
//! Selcom's hosted Checkout (`/v1/checkout/...`) and Wallet C2B
//! (`/v1/wallet/pushussd`, `/v1/c2b/query-status`) APIs. Credentials:
//! `api_key`, `api_secret`, `vendor`. Every request is signed: an HMAC-SHA256
//! over `"timestamp=<ts>&<field>=<value>..."` (fields in the order they are
//! sent) keyed on `api_secret`, carried in the `Digest`/`Signed-Fields`
//! headers. The webhook body carries no signature, so it is authenticated by
//! re-querying the order status.

use base64::Engine;
use hmac::{Hmac, Mac};
use serde_json::Value;
use sha2::Sha256;

use super::{
    PaymentRequest, PaymentResponse, PaymentResult, ProviderError, PushRequest, PushResponse,
    WebhookError, WebhookEvent, STATUS_FAILED, STATUS_PENDING, STATUS_SUCCEEDED,
};
use crate::config::PaymentProviderConfig;

const BASE_URL: &str = "https://apigw.selcommobile.com";
/// Selcom timestamps are in East Africa Time (UTC+3).
const EAT_OFFSET_SECS: i32 = 3 * 3600;

pub(crate) struct Selcom {
    api_key: String,
    api_secret: String,
    vendor: String,
    base: String,
    webhook_url: String,
}

/// One request field, kept in order (the signature depends on it).
struct Field {
    key: &'static str,
    value: Value,
}

impl Field {
    fn text(key: &'static str, value: impl Into<String>) -> Self {
        Field {
            key,
            value: Value::String(value.into()),
        }
    }

    fn number(key: &'static str, value: i64) -> Self {
        Field {
            key,
            value: Value::Number(value.into()),
        }
    }

    /// How the value enters the signature and query string.
    fn signed(&self) -> String {
        match &self.value {
            Value::String(s) => s.clone(),
            other => other.to_string(),
        }
    }
}

impl Selcom {
    pub(crate) fn new(config: &PaymentProviderConfig) -> Option<Self> {
        let cred = |k: &str| {
            config
                .credentials
                .get(k)
                .map(|v| v.trim().to_string())
                .unwrap_or_default()
        };
        let (api_key, api_secret, vendor) = (cred("api_key"), cred("api_secret"), cred("vendor"));
        if api_key.is_empty() || api_secret.is_empty() || vendor.is_empty() {
            return None;
        }
        let base = if config.base_url.trim().is_empty() {
            BASE_URL.to_string()
        } else {
            config.base_url.trim_end_matches('/').to_string()
        };
        Some(Self {
            api_key,
            api_secret,
            vendor,
            base,
            webhook_url: config.webhook_url.clone(),
        })
    }

    fn error(&self, code: impl Into<String>, message: impl Into<String>) -> ProviderError {
        ProviderError {
            provider: "selcom".into(),
            code: code.into(),
            message: message.into(),
        }
    }

    /// The signed request headers for `fields` (Go's `sign`).
    fn sign(&self, fields: &[Field]) -> Vec<(String, String)> {
        let timestamp = chrono::Utc::now()
            .with_timezone(&chrono::FixedOffset::east_opt(EAT_OFFSET_SECS).unwrap())
            .to_rfc3339_opts(chrono::SecondsFormat::Secs, false);

        let mut data = format!("timestamp={timestamp}");
        let mut keys = Vec::with_capacity(fields.len());
        for field in fields {
            data.push_str(&format!("&{}={}", field.key, field.signed()));
            keys.push(field.key);
        }
        let mut mac = Hmac::<Sha256>::new_from_slice(self.api_secret.as_bytes()).expect("hmac key");
        mac.update(data.as_bytes());
        let base64 = base64::engine::general_purpose::STANDARD;

        vec![
            (
                "Authorization".into(),
                format!("SELCOM {}", base64.encode(&self.api_key)),
            ),
            ("Digest-Method".into(), "HS256".into()),
            ("Digest".into(), base64.encode(mac.finalize().into_bytes())),
            ("Timestamp".into(), timestamp),
            ("Signed-Fields".into(), keys.join(",")),
        ]
    }

    /// A signed POST with a JSON body built in field order.
    async fn post(&self, path: &str, fields: &[Field]) -> Result<Value, ProviderError> {
        let mut body = String::from("{");
        for (i, field) in fields.iter().enumerate() {
            if i > 0 {
                body.push(',');
            }
            body.push_str(&serde_json::to_string(field.key).unwrap_or_default());
            body.push(':');
            body.push_str(&serde_json::to_string(&field.value).unwrap_or_default());
        }
        body.push('}');

        let mut request = reqwest::Client::new()
            .post(format!("{}{path}", self.base))
            .header("content-type", "application/json")
            .body(body);
        for (name, value) in self.sign(fields) {
            request = request.header(name, value);
        }
        self.send(request).await
    }

    /// A signed GET with the fields as the query string.
    async fn get(&self, path: &str, fields: &[Field]) -> Result<Value, ProviderError> {
        let query: Vec<(String, String)> = fields
            .iter()
            .map(|f| (f.key.to_string(), f.signed()))
            .collect();
        let mut request = reqwest::Client::new()
            .get(format!("{}{path}", self.base))
            .query(&query);
        for (name, value) in self.sign(fields) {
            request = request.header(name, value);
        }
        self.send(request).await
    }

    async fn send(&self, request: reqwest::RequestBuilder) -> Result<Value, ProviderError> {
        let response = request
            .send()
            .await
            .map_err(|e| self.error("", e.to_string()))?;
        response
            .json::<Value>()
            .await
            .map_err(|e| self.error("", e.to_string()))
    }

    /// Opens a hosted Checkout order and returns its gateway URL.
    pub(crate) async fn create_payment(
        &self,
        req: &PaymentRequest,
    ) -> Result<PaymentResponse, ProviderError> {
        let c = &req.customer;
        if c.name.is_empty() || c.email.is_empty() || c.phone.is_empty() {
            return Err(self.error("", "selcom needs customer name, email and phone"));
        }
        let base64 = base64::engine::general_purpose::STANDARD;
        let remarks = if req.description.is_empty() {
            "None"
        } else {
            &req.description
        };

        let mut fields = vec![
            Field::text("vendor", &self.vendor),
            Field::text("order_id", &req.reference),
            Field::text("buyer_email", &c.email),
            Field::text("buyer_name", &c.name),
            Field::text("buyer_phone", clean_phone(&c.phone)),
            Field::number("amount", req.amount.round() as i64),
            Field::text("currency", req.currency.to_uppercase()),
        ];
        if !req.return_url.is_empty() {
            fields.push(Field::text("redirect_url", base64.encode(&req.return_url)));
        }
        let cancel = if req.cancel_url.is_empty() {
            &req.return_url
        } else {
            &req.cancel_url
        };
        if !cancel.is_empty() {
            fields.push(Field::text("cancel_url", base64.encode(cancel)));
        }
        let webhook = if self.webhook_url.is_empty() {
            ""
        } else {
            &self.webhook_url
        };
        if !webhook.is_empty() {
            fields.push(Field::text("webhook", base64.encode(webhook)));
        }
        fields.push(Field::text("buyer_remarks", remarks));
        fields.push(Field::text("merchant_remarks", remarks));
        fields.push(Field::number("no_of_items", 1));

        let raw = self
            .post("/v1/checkout/create-order-minimal", &fields)
            .await?;
        let data = raw["data"].get(0);
        if !envelope_ok(&raw) || data.is_none() {
            return Err(self.envelope_error(&raw));
        }
        let link = decode_b64_or_raw(
            data.unwrap()["payment_gateway_url"]
                .as_str()
                .unwrap_or_default(),
        );
        if link.is_empty() {
            return Err(self.error(result_code(&raw), "checkout link was not returned"));
        }
        Ok(PaymentResponse {
            provider: "selcom".into(),
            reference: req.reference.clone(),
            provider_ref: raw["reference"].as_str().unwrap_or_default().into(),
            checkout_url: link,
            status: STATUS_PENDING.into(),
            raw,
        })
    }

    /// Triggers a wallet debit prompt on the customer's phone.
    pub(crate) async fn push_ussd(&self, req: &PushRequest) -> Result<PushResponse, ProviderError> {
        let utility = if req.description.is_empty() {
            &req.reference
        } else {
            &req.description
        };
        let fields = vec![
            Field::text("transid", &req.reference),
            Field::text("utilityref", utility),
            Field::number("amount", req.amount.round() as i64),
            Field::text("vendor", &self.vendor),
            Field::text("msisdn", clean_phone(&req.phone)),
        ];
        let raw = self.post("/v1/wallet/pushussd", &fields).await?;
        if !envelope_ok(&raw) {
            return Err(self.envelope_error(&raw));
        }
        let reference = non_empty(raw["reference"].as_str(), &req.reference);
        let instructions = non_empty(
            raw["message"].as_str(),
            "Enter your mobile money PIN on your phone to complete the payment.",
        );
        Ok(PushResponse {
            provider: "selcom".into(),
            reference: req.reference.clone(),
            provider_ref: reference,
            status: STATUS_PENDING.into(),
            instructions,
            raw,
        })
    }

    /// Checks the checkout order status, falling back to the wallet C2B status
    /// query for a push-originated reference.
    pub(crate) async fn verify_payment(
        &self,
        reference: &str,
    ) -> Result<PaymentResult, ProviderError> {
        if reference.is_empty() {
            return Err(self.error("", "selcom looks payments up by reference"));
        }
        let order = self
            .get(
                "/v1/checkout/order-status",
                &[Field::text("order_id", reference)],
            )
            .await;
        if let Ok(raw) = &order {
            if envelope_ok(raw) {
                if let Some(data) = raw["data"].get(0) {
                    let (status, failure_reason) =
                        selcom_status(data["payment_status"].as_str().unwrap_or_default());
                    return Ok(PaymentResult {
                        reference: non_empty(data["order_id"].as_str(), reference),
                        provider_ref: non_empty(
                            data["reference"].as_str(),
                            raw["reference"].as_str().unwrap_or_default(),
                        ),
                        provider_payment_id: data["transid"].as_str().unwrap_or_default().into(),
                        status: status.into(),
                        amount: data["amount"]
                            .as_f64()
                            .or_else(|| data["amount"].as_str().and_then(|s| s.trim().parse().ok()))
                            .unwrap_or(0.0),
                        currency: "TZS".into(),
                        method: data["channel"].as_str().unwrap_or_default().into(),
                        failure_reason,
                        raw: raw.clone(),
                    });
                }
            }
        }
        self.query_c2b_status(reference).await
    }

    /// Checks a wallet push via `/v1/c2b/query-status`.
    async fn query_c2b_status(&self, transid: &str) -> Result<PaymentResult, ProviderError> {
        let raw = self
            .get("/v1/c2b/query-status", &[Field::text("transid", transid)])
            .await?;
        if !envelope_ok(&raw) {
            return Err(self.envelope_error(&raw));
        }
        let result = raw["result"].as_str().unwrap_or_default();
        let message = raw["message"].as_str().unwrap_or_default();
        let upper = message.to_uppercase();

        let (status, failure_reason) = if result.eq_ignore_ascii_case("SUCCESS")
            || upper.contains("COMPLETE")
            || upper.contains("CONFIRMED")
        {
            (STATUS_SUCCEEDED, String::new())
        } else if result.eq_ignore_ascii_case("FAILED") {
            (STATUS_FAILED, non_empty(raw["message"].as_str(), "failed"))
        } else {
            (STATUS_PENDING, String::new())
        };

        Ok(PaymentResult {
            reference: non_empty(raw["reference"].as_str(), transid),
            provider_payment_id: transid.into(),
            status: status.into(),
            currency: "TZS".into(),
            failure_reason,
            raw,
            ..Default::default()
        })
    }

    /// Authenticates a Selcom callback by re-querying the order status. The
    /// callback body carries no signature, so the order status (an authenticated
    /// request) is the source of truth.
    pub(crate) async fn parse_webhook(&self, body: &[u8]) -> Result<WebhookEvent, WebhookError> {
        let payload: Value =
            serde_json::from_slice(body).map_err(|_| WebhookError::InvalidWebhook)?;
        let order_id = payload["order_id"].as_str().unwrap_or_default();
        if order_id.is_empty() {
            return Err(WebhookError::InvalidWebhook);
        }
        let result = self
            .verify_payment(order_id)
            .await
            .map_err(|e| WebhookError::Internal(e.to_string()))?;
        Ok(WebhookEvent {
            provider: "selcom".into(),
            event_type: "order.status".into(),
            result: Some(result),
            ack: None,
        })
    }

    fn envelope_error(&self, raw: &Value) -> ProviderError {
        let message = raw["message"]
            .as_str()
            .filter(|s| !s.is_empty())
            .or_else(|| raw["result"].as_str().filter(|s| !s.is_empty()))
            .unwrap_or("request rejected");
        self.error(result_code(raw), message)
    }
}

/// A Selcom envelope is OK when `resultcode` is `"000"`.
fn envelope_ok(raw: &Value) -> bool {
    result_code(raw) == "000"
}

fn result_code(raw: &Value) -> String {
    raw["resultcode"].as_str().unwrap_or_default().to_string()
}

/// The first non-empty of `value` and `fallback`.
fn non_empty(value: Option<&str>, fallback: &str) -> String {
    match value {
        Some(v) if !v.is_empty() => v.to_string(),
        _ => fallback.to_string(),
    }
}

/// Strips `+`, spaces and dashes from a phone number.
fn clean_phone(phone: &str) -> String {
    phone
        .chars()
        .filter(|c| !matches!(c, '+' | ' ' | '-'))
        .collect()
}

/// Decodes a base64 URL, or returns it unchanged when it already is a URL.
fn decode_b64_or_raw(value: &str) -> String {
    let value = value.trim();
    if value.starts_with("http://") || value.starts_with("https://") {
        return value.to_string();
    }
    base64::engine::general_purpose::STANDARD
        .decode(value)
        .ok()
        .and_then(|bytes| String::from_utf8(bytes).ok())
        .unwrap_or_default()
}

/// Maps a Selcom checkout `payment_status` to a status and failure reason.
fn selcom_status(status: &str) -> (&'static str, String) {
    match status.to_uppercase().as_str() {
        "COMPLETED" => (STATUS_SUCCEEDED, String::new()),
        s @ ("CANCELLED" | "USERCANCELLED" | "REJECTED" | "EXPIRED" | "FAILED") => {
            (STATUS_FAILED, s.to_lowercase())
        }
        _ => (STATUS_PENDING, String::new()),
    }
}
