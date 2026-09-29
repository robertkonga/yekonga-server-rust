//! Payment webhooks with a pluggable verifier.
//!
//! Go's `payment.Controller` verifies each provider's webhook signature with
//! the provider's own SDK and credentials. Those providers aren't ported, so
//! this module keeps the framework around the webhook — the routes, the safe
//! update of the `Payment` record, and the post-processing hook — and leaves
//! verification to a function the application registers with
//! [`Yekonga::set_payment_verify`]. The verifier receives the raw request and
//! returns a [`WebhookEvent`] it has authenticated, or a [`WebhookError`]
//! saying why it rejected the request; without one registered, every webhook
//! is refused (`501`), so an unverified notification can never settle a payment.
//!
//! The flow mirrors Go's `paymentWebhookHandler`:
//! 1. The routes `<webhookRoute>/:provider` and `<webhookRoute>/:provider/:tenantId`
//!    (default `webhookRoute` is `/payment/webhook`) receive GET and POST and
//!    are public.
//! 2. The registered verifier authenticates the request into a [`WebhookEvent`].
//! 3. When the event carries a [`PaymentResult`], [`apply_payment_result`]
//!    updates the matching `Payment` record — never moving it back from
//!    succeeded/refunded, and saving a success whose amount or currency differs
//!    from the record as failed instead, so a tampered or partial payment can't
//!    settle an invoice. Applying the same event twice is a no-op.
//! 4. The function registered with [`Yekonga::set_payment_webhook`] runs with
//!    the event and the updated record; returning an error answers `500` so the
//!    gateway retries (the retry re-runs the function, so it must be idempotent).
//! 5. The event's `ack` is written as the response body (an empty `200` when
//!    it is `None`).

use std::sync::Arc;

use axum::http::{HeaderMap, Method};
use bytes::Bytes;
use serde_json::{json, Map, Value};

use crate::app::{BoxFuture, Yekonga};
use crate::config::PaymentProviderConfig;
use crate::db::values::is_empty;
use crate::db::DataMap;
use crate::request::Request;
use crate::response::Response;

const PAYMENT_MODEL: &str = "Payment";
const DEFAULT_WEBHOOK_ROUTE: &str = "/payment/webhook";
const WEBHOOK_METADATA_KEY: &str = "lastWebhook";
/// Amounts within this of the record's are treated as equal (rounding).
const AMOUNT_MATCH_TOLERANCE: f64 = 0.01;

/// Payment status values, matching Go's `payment.Status`.
pub const STATUS_PENDING: &str = "PENDING";
pub const STATUS_SUCCEEDED: &str = "SUCCESS";
pub const STATUS_FAILED: &str = "FAILED";
pub const STATUS_REFUNDED: &str = "REFUNDED";

/// The verified state of a payment carried by a webhook (Go's
/// `payment.PaymentResult`). Always compare `amount`/`currency` with what you
/// expected before treating a payment as settled — [`apply_payment_result`]
/// does this against the stored record.
#[derive(Clone, Debug, Default)]
pub struct PaymentResult {
    /// Our reference — the `Payment` record id.
    pub reference: String,
    /// The gateway's order/checkout id.
    pub provider_ref: String,
    /// The id needed to refund (capture/transaction id, confirmation code).
    pub provider_payment_id: String,
    /// One of the `STATUS_*` values.
    pub status: String,
    pub amount: f64,
    pub currency: String,
    /// The channel/method the gateway reported (e.g. `card`, `mpesa`).
    pub method: String,
    pub failure_reason: String,
    /// The raw provider payload, stored on the record's `metadata.lastWebhook`.
    pub raw: Value,
}

/// An authenticated provider notification (Go's `payment.WebhookEvent`).
#[derive(Clone, Debug, Default)]
pub struct WebhookEvent {
    pub provider: String,
    /// The gateway's event name.
    pub event_type: String,
    /// The payment state, or `None` for events that carry none.
    pub result: Option<PaymentResult>,
    /// The body the gateway expects back, or `None` for an empty `200`.
    pub ack: Option<Value>,
}

impl WebhookEvent {
    /// True only when the result reports a succeeded payment.
    pub fn success(&self) -> bool {
        self.result
            .as_ref()
            .map(|r| r.status == STATUS_SUCCEEDED)
            .unwrap_or(false)
    }
}

mod selcom;
mod stripe;

/// The customer a payment is for (Go's `payment.Customer`).
#[derive(Clone, Debug, Default)]
pub struct Customer {
    pub name: String,
    pub email: String,
    pub phone: String,
    pub country_code: String,
}

/// A request to open a hosted checkout (Go's `payment.PaymentRequest`).
#[derive(Clone, Debug, Default)]
pub struct PaymentRequest {
    /// The merchant reference — usually the `Payment` record id.
    pub reference: String,
    pub amount: f64,
    /// ISO 4217 currency.
    pub currency: String,
    pub description: String,
    pub customer: Customer,
    /// Where the payer lands after paying.
    pub return_url: String,
    /// Where the payer lands after cancelling (falls back to `return_url`).
    pub cancel_url: String,
    /// Echoed back by providers that support it.
    pub metadata: Map<String, Value>,
}

/// The outcome of [`create_payment`](Yekonga::create_payment).
#[derive(Clone, Debug)]
pub struct PaymentResponse {
    pub provider: String,
    pub reference: String,
    /// The gateway's id for the checkout/order.
    pub provider_ref: String,
    /// The URL to redirect the payer to.
    pub checkout_url: String,
    pub status: String,
    pub raw: Value,
}

/// A request to trigger a direct mobile-money charge (Go's `payment.PushRequest`):
/// the gateway prompts the customer on their phone to approve with their PIN.
#[derive(Clone, Debug, Default)]
pub struct PushRequest {
    /// The merchant reference — usually the `Payment` record id.
    pub reference: String,
    pub amount: f64,
    pub currency: String,
    /// The MSISDN the prompt is sent to.
    pub phone: String,
    pub description: String,
}

/// The outcome of [`push_ussd`](Yekonga::push_ussd): the prompt was delivered,
/// not yet approved — poll `verify_payment` or wait for the webhook.
#[derive(Clone, Debug)]
pub struct PushResponse {
    pub provider: String,
    pub reference: String,
    pub provider_ref: String,
    pub status: String,
    /// Human text from the gateway, e.g. "enter your PIN".
    pub instructions: String,
    pub raw: Value,
}

/// A request to refund a settled payment (Go's `payment.RefundRequest`).
#[derive(Clone, Debug, Default)]
pub struct RefundRequest {
    /// The `provider_payment_id` from the settled payment.
    pub provider_payment_id: String,
    /// `0` means a full refund where the gateway supports it.
    pub amount: f64,
    pub currency: String,
    pub reason: String,
}

/// The outcome of [`refund`](Yekonga::refund).
#[derive(Clone, Debug)]
pub struct RefundResult {
    pub provider: String,
    pub provider_refund_id: String,
    pub status: String,
    pub raw: Value,
}

/// A gateway call failure (Go's `payment.ProviderError`).
#[derive(Clone, Debug)]
pub struct ProviderError {
    pub provider: String,
    pub code: String,
    pub message: String,
}

impl std::fmt::Display for ProviderError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if self.code.is_empty() {
            write!(f, "{}: {}", self.provider, self.message)
        } else {
            write!(f, "{} [{}]: {}", self.provider, self.code, self.message)
        }
    }
}

impl std::error::Error for ProviderError {}

/// A configured payment provider client. New providers add a variant and the
/// matching arms below.
enum Provider {
    Stripe(stripe::Stripe),
    Selcom(selcom::Selcom),
}

impl Provider {
    /// Builds the client for a configured gateway, or `None` when the provider
    /// isn't ported or its credentials are missing.
    fn from_config(config: &PaymentProviderConfig) -> Option<Self> {
        match config.provider.as_str() {
            "stripe" => stripe::Stripe::new(config).map(Provider::Stripe),
            "selcom" => selcom::Selcom::new(config).map(Provider::Selcom),
            _ => None,
        }
    }

    fn not_supported(&self, operation: &str) -> ProviderError {
        ProviderError {
            provider: self.name().into(),
            code: String::new(),
            message: format!("{} does not support {operation}", self.name()),
        }
    }

    fn name(&self) -> &'static str {
        match self {
            Provider::Stripe(_) => "stripe",
            Provider::Selcom(_) => "selcom",
        }
    }

    async fn create_payment(&self, req: &PaymentRequest) -> Result<PaymentResponse, ProviderError> {
        match self {
            Provider::Stripe(stripe) => stripe.create_payment(req).await,
            Provider::Selcom(selcom) => selcom.create_payment(req).await,
        }
    }

    async fn verify_payment(&self, provider_ref: &str) -> Result<PaymentResult, ProviderError> {
        match self {
            Provider::Stripe(stripe) => stripe.verify_payment(provider_ref).await,
            Provider::Selcom(selcom) => selcom.verify_payment(provider_ref).await,
        }
    }

    async fn push_ussd(&self, req: &PushRequest) -> Result<PushResponse, ProviderError> {
        match self {
            Provider::Selcom(selcom) => selcom.push_ussd(req).await,
            _ => Err(self.not_supported("a mobile-money push")),
        }
    }

    async fn refund(&self, req: &RefundRequest) -> Result<RefundResult, ProviderError> {
        match self {
            Provider::Stripe(stripe) => stripe.refund(req).await,
            Provider::Selcom(_) => Err(self.not_supported("refunds")),
        }
    }

    async fn parse_webhook(
        &self,
        headers: &axum::http::HeaderMap,
        body: &[u8],
    ) -> Result<WebhookEvent, WebhookError> {
        match self {
            Provider::Stripe(stripe) => {
                let signature = headers
                    .get("stripe-signature")
                    .and_then(|v| v.to_str().ok())
                    .unwrap_or_default();
                stripe.parse_webhook(signature, body)
            }
            Provider::Selcom(selcom) => selcom.parse_webhook(body).await,
        }
    }
}

/// Why a verifier rejected a webhook. Each maps to the status Go returns.
#[derive(Debug)]
pub enum WebhookError {
    /// The signature/authentication failed (`401`).
    InvalidWebhook,
    /// The provider isn't one this app handles (`404`).
    UnknownProvider,
    /// The body couldn't be parsed (`400`).
    InvalidRequest,
    /// Anything else, so the gateway retries (`500`).
    Internal(String),
}

impl WebhookError {
    fn status(&self) -> u16 {
        match self {
            WebhookError::InvalidWebhook => 401,
            WebhookError::UnknownProvider => 404,
            WebhookError::InvalidRequest => 400,
            WebhookError::Internal(_) => 500,
        }
    }
}

/// The raw request handed to a [`set_payment_verify`](Yekonga::set_payment_verify)
/// function. Payment gateways sign the raw body, so `body` is the untouched
/// bytes; `tenant_id` is empty for a webhook on the server's own account.
pub struct PaymentVerifyContext {
    pub provider: String,
    pub tenant_id: String,
    pub method: Method,
    pub headers: HeaderMap,
    pub body: Bytes,
}

/// What a [`set_payment_webhook`](Yekonga::set_payment_webhook) function
/// receives after the record has been updated.
pub struct PaymentWebhookData {
    /// The authenticated event; `event.success()` reports a settled payment.
    pub event: WebhookEvent,
    /// The `Payment` record after the webhook was applied; `None` when none
    /// matched or the event carried no payment state.
    pub payment: Option<DataMap>,
}

pub type PaymentVerifyFn = Arc<
    dyn Fn(PaymentVerifyContext) -> BoxFuture<Result<WebhookEvent, WebhookError>> + Send + Sync,
>;
pub type PaymentWebhookFn =
    Arc<dyn Fn(PaymentWebhookData) -> BoxFuture<Result<(), String>> + Send + Sync>;

/// The registered payment hooks.
#[derive(Default, Clone)]
pub(crate) struct PaymentHooks {
    pub verify: Option<PaymentVerifyFn>,
    pub webhook: Option<PaymentWebhookFn>,
}

impl Yekonga {
    /// Registers the function that authenticates payment webhooks. It receives
    /// the raw request as a [`PaymentVerifyContext`] and returns the
    /// [`WebhookEvent`] it verified, or a [`WebhookError`]. This is where
    /// provider signature checking lives — without it, every webhook is
    /// refused.
    pub fn set_payment_verify(
        &self,
        function: impl Fn(PaymentVerifyContext) -> BoxFuture<Result<WebhookEvent, WebhookError>>
            + Send
            + Sync
            + 'static,
    ) {
        self.payment_hooks()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .verify = Some(Arc::new(function));
    }

    /// Registers the function that runs after an authenticated webhook has
    /// updated the `Payment` record (Go's `SetPaymentWebhook`). Returning an
    /// error answers the gateway `500` so it retries, so the function must be
    /// idempotent.
    pub fn set_payment_webhook(
        &self,
        function: impl Fn(PaymentWebhookData) -> BoxFuture<Result<(), String>> + Send + Sync + 'static,
    ) {
        self.payment_hooks()
            .write()
            .unwrap_or_else(|e| e.into_inner())
            .webhook = Some(Arc::new(function));
    }

    /// The webhook route prefix (`apiGateway.payment.webhookRoute`, default
    /// `/payment/webhook`), normalized to a single leading slash and no
    /// trailing one.
    pub(crate) fn payment_webhook_route(&self) -> String {
        let route = self.config().api_gateway.payment.webhook_route.trim();
        let route = if route.is_empty() {
            DEFAULT_WEBHOOK_ROUTE
        } else {
            route
        };
        format!("/{}", route.trim_matches('/'))
    }

    /// Whether payments are in use: providers configured, the payment module
    /// on, or tenant billing on (Go's `paymentEnabled`).
    fn payment_enabled(&self) -> bool {
        let config = self.config();
        !config.api_gateway.payment.providers.is_empty()
            || config.has_payment_module
            || (config.has_tenant && config.has_tenant_billing)
    }

    /// The built-in client for the named configured provider (`apiGateway.payment.providers`),
    /// or `None` when it isn't configured or isn't a ported provider. Only
    /// Stripe is ported so far.
    fn payment_provider(&self, name: &str) -> Option<Provider> {
        self.config()
            .api_gateway
            .payment
            .providers
            .iter()
            .find(|p| p.provider.eq_ignore_ascii_case(name))
            .and_then(Provider::from_config)
    }

    /// Opens a hosted checkout with the named provider (Go's `CreatePayment`).
    pub async fn create_payment(
        &self,
        provider: &str,
        request: PaymentRequest,
    ) -> Result<PaymentResponse, ProviderError> {
        self.require_provider(provider)?
            .create_payment(&request)
            .await
    }

    /// Asks the named provider for a payment's current state, by the gateway's
    /// checkout/order reference (Go's `VerifyPayment`).
    pub async fn verify_payment(
        &self,
        provider: &str,
        provider_ref: &str,
    ) -> Result<PaymentResult, ProviderError> {
        self.require_provider(provider)?
            .verify_payment(provider_ref)
            .await
    }

    /// Triggers a direct mobile-money charge through the named provider (Go's
    /// `PushUSSD`). Only providers with a push flow (e.g. Selcom) support it.
    pub async fn push_ussd(
        &self,
        provider: &str,
        request: PushRequest,
    ) -> Result<PushResponse, ProviderError> {
        self.require_provider(provider)?.push_ussd(&request).await
    }

    /// Refunds a settled payment through the named provider (Go's `Refund`).
    pub async fn refund(
        &self,
        provider: &str,
        request: RefundRequest,
    ) -> Result<RefundResult, ProviderError> {
        self.require_provider(provider)?.refund(&request).await
    }

    fn require_provider(&self, provider: &str) -> Result<Provider, ProviderError> {
        self.payment_provider(provider)
            .ok_or_else(|| ProviderError {
                provider: provider.to_string(),
                code: String::new(),
                message: format!("payment provider {provider:?} is not configured or not ported"),
            })
    }
}

/// Serves provider notifications when payments are in use (Go's
/// `initializePaymentRoutes`).
pub(crate) fn register_routes(app: &Yekonga) {
    if !app.payment_enabled() {
        return;
    }

    let route = app.payment_webhook_route();
    let path = format!("{route}/:provider");
    let tenant_path = format!("{path}/:tenantId");

    for p in [path.clone(), tenant_path.clone()] {
        app.get(&p, |req, res| async move { handle(req, res).await });
        app.post(&p, |req, res| async move { handle(req, res).await });
    }
    app.set_public_route(format!("{route}/*"));
    app.set_public_route(format!("{route}/*/*"));

    tracing::info!(path = %app.append_base_url(&path), "payment webhook enabled");
}

/// Authenticates a provider notification and applies the payment state it
/// carries to the matching `Payment` record (Go's `paymentWebhookHandler`).
async fn handle(req: Request, res: Response) {
    let app = req.app().clone();
    let provider = req.param("provider");
    let tenant_id = req.param("tenantId");

    let verify = app
        .payment_hooks()
        .read()
        .unwrap_or_else(|e| e.into_inner())
        .verify
        .clone();

    // A registered verifier wins; otherwise the built-in client for the
    // configured provider authenticates the request. With neither, refuse
    // rather than trust an unauthenticated webhook.
    let event = if let Some(verify) = verify {
        let context = PaymentVerifyContext {
            provider: provider.clone(),
            tenant_id: tenant_id.clone(),
            method: req.method().clone(),
            headers: req.headers().clone(),
            body: req.raw_body().clone(),
        };
        match verify(context).await {
            Ok(event) => event,
            Err(error) => {
                tracing::error!(provider, ?error, "payment webhook rejected");
                return reject(&req, &res, error.status());
            }
        }
    } else if let Some(client) = app.payment_provider(&provider) {
        match client.parse_webhook(req.headers(), req.raw_body()).await {
            Ok(event) => event,
            Err(error) => {
                tracing::error!(provider, ?error, "payment webhook rejected");
                return reject(&req, &res, error.status());
            }
        }
    } else {
        tracing::error!(
            provider,
            "payment webhook received but no verifier is registered"
        );
        return reject(&req, &res, 501);
    };

    let mut record = None;
    if event.result.is_some() {
        match apply_payment_result(&app, &req, &event, &tenant_id).await {
            Ok(applied) => record = applied,
            Err(error) => {
                // 500 makes the gateway retry later.
                tracing::error!(provider, %error, "payment webhook failed");
                return reject(&req, &res, 500);
            }
        }
    }

    let webhook = app
        .payment_hooks()
        .read()
        .unwrap_or_else(|e| e.into_inner())
        .webhook
        .clone();
    if let Some(webhook) = webhook {
        let data = PaymentWebhookData {
            event: event.clone(),
            payment: record,
        };
        if let Err(error) = webhook(data).await {
            tracing::error!(provider, %error, "payment webhook function failed");
            return reject(&req, &res, 500);
        }
    }

    match event.ack {
        Some(ack) => res.json(&ack),
        None => res.text(""),
    }
}

fn reject(_req: &Request, res: &Response, status: u16) {
    // The serve loop records error responses for the error guard, so this only
    // needs to set the status and a plain body.
    res.status(status);
    res.text(status_text(status));
}

fn status_text(status: u16) -> &'static str {
    match status {
        400 => "Bad Request",
        401 => "Unauthorized",
        404 => "Not Found",
        501 => "Not Implemented",
        _ => "Internal Server Error",
    }
}

/// Updates the `Payment` record an authenticated webhook refers to and returns
/// it (`None` when no record matches). Port of Go's `applyPaymentResult`: a
/// payment never moves back from succeeded/refunded; a success whose amount or
/// currency differs from the record is saved as failed instead; and re-applying
/// the same state is a no-op, so repeated deliveries are safe.
///
/// `tenant_id` is the tenant whose credentials authenticated the webhook (empty
/// for the server's). Only payments made on those same credentials match, so
/// one tenant can't settle another's payment.
pub(crate) async fn apply_payment_result(
    app: &Yekonga,
    req: &Request,
    event: &WebhookEvent,
    tenant_id: &str,
) -> Result<Option<DataMap>, String> {
    let result = event.result.as_ref().expect("caller checked result is set");
    let provider = event.provider.to_lowercase();

    let record = find_payment_for_result(app, req, &provider, result).await?;
    let Some(record) = record.filter(|r| payment_made_with(r, tenant_id)) else {
        // Retrying won't make the record appear, so acknowledge the event.
        tracing::error!(provider, reference = %result.reference, "payment webhook: no payment matches");
        return Ok(None);
    };

    let id = payment_record_id(&record);
    let current = string_of(&record, "status");
    let mut next = result.status.clone();

    // Never move back from a terminal state (refund is the only step past success).
    if current == STATUS_REFUNDED || (current == STATUS_SUCCEEDED && next != STATUS_REFUNDED) {
        return Ok(Some(record));
    }

    // A success that doesn't match the invoice is recorded as a failure.
    let mut failure_reason = result.failure_reason.clone();
    if next == STATUS_SUCCEEDED {
        if let Some(reason) = payment_mismatch(&record, result) {
            next = STATUS_FAILED.to_string();
            failure_reason = reason;
        }
    }

    // Idempotent: the same status and provider payment id means nothing to do.
    let stored_payment_id = string_of(&record, "providerPaymentId");
    if next == current
        && (result.provider_payment_id.is_empty()
            || result.provider_payment_id == stored_payment_id)
    {
        return Ok(Some(record));
    }

    let mut metadata = payment_metadata(&record);
    metadata.insert(
        WEBHOOK_METADATA_KEY.into(),
        json!({
            "type": event.event_type,
            "method": result.method,
            "amount": result.amount,
            "currency": result.currency,
            "raw": result.raw,
        }),
    );

    let now = crate::db::values::now_string();
    let mut update = Map::new();
    update.insert("status".into(), Value::String(next.clone()));
    update.insert("metadata".into(), Value::Object(metadata));
    update.insert("updatedAt".into(), Value::String(now.clone()));

    if !result.provider_payment_id.is_empty() {
        update.insert(
            "providerPaymentId".into(),
            Value::String(result.provider_payment_id.clone()),
        );
    }
    if !result.provider_ref.is_empty() && string_of(&record, "providerReference").is_empty() {
        update.insert(
            "providerReference".into(),
            Value::String(result.provider_ref.clone()),
        );
    }
    if let Some(method) = normalize_payment_method(&result.method) {
        update.insert("paymentMethod".into(), Value::String(method.into()));
    }
    if !failure_reason.is_empty() {
        update.insert("failureReason".into(), Value::String(failure_reason));
    }
    match next.as_str() {
        STATUS_SUCCEEDED => {
            update.insert("paidAt".into(), Value::String(now));
        }
        STATUS_REFUNDED => {
            update.insert("refundedAt".into(), Value::String(now));
        }
        _ => {}
    }

    let updated = app
        .query(PAYMENT_MODEL)
        .map_err(|e| e.to_string())?
        .set_request(req)
        .skip_tenant()
        .where_("id", id)
        .update(Value::Object(update))
        .await
        .map_err(|e| e.to_string())?;

    Ok(updated)
}

/// Looks the record up by our reference (the `Payment` id) first and the
/// gateway's order id second, always within the same provider.
async fn find_payment_for_result(
    app: &Yekonga,
    req: &Request,
    provider: &str,
    result: &PaymentResult,
) -> Result<Option<DataMap>, String> {
    for (field, value) in [
        ("id", &result.reference),
        ("providerReference", &result.provider_ref),
    ] {
        if value.is_empty() {
            continue;
        }
        let record = app
            .query(PAYMENT_MODEL)
            .map_err(|e| e.to_string())?
            .set_request(req)
            .skip_tenant()
            .skip_before_commit()
            .where_(field, value.clone())
            .where_("provider", provider)
            .find_one()
            .await
            .map_err(|e| e.to_string())?;
        if record.as_ref().is_some_and(|r| !r.is_empty()) {
            return Ok(record);
        }
    }
    Ok(None)
}

/// Whether the payment was made on `tenant_id`'s own gateway account, or the
/// server's when `tenant_id` is empty.
fn payment_made_with(record: &DataMap, tenant_id: &str) -> bool {
    let made_with_tenant = string_of(record, "providerConfig") == "tenant";
    if tenant_id.is_empty() {
        return !made_with_tenant;
    }
    made_with_tenant && string_of(record, "tenantId") == tenant_id
}

/// A reason the result doesn't match the record's amount/currency, if any.
fn payment_mismatch(record: &DataMap, result: &PaymentResult) -> Option<String> {
    let expected_amount = record.get("amount").and_then(Value::as_f64).unwrap_or(0.0);
    if result.amount > 0.0 && (result.amount - expected_amount).abs() > AMOUNT_MATCH_TOLERANCE {
        return Some(format!(
            "amount mismatch: expected {expected_amount:.2}, provider reported {:.2}",
            result.amount
        ));
    }

    let expected_currency = string_of(record, "currency");
    if !result.currency.is_empty()
        && !expected_currency.is_empty()
        && !result.currency.eq_ignore_ascii_case(&expected_currency)
    {
        return Some(format!(
            "currency mismatch: expected {expected_currency}, provider reported {}",
            result.currency
        ));
    }

    None
}

/// The record's `metadata` as a map (it may be stored as a JSON string).
fn payment_metadata(record: &DataMap) -> Map<String, Value> {
    match record.get("metadata") {
        Some(Value::Object(map)) => map.clone(),
        Some(Value::String(text)) => serde_json::from_str(text).unwrap_or_default(),
        _ => Map::new(),
    }
}

/// The record id, from `id` or `_id`.
fn payment_record_id(record: &DataMap) -> String {
    for key in ["id", "_id"] {
        match record.get(key) {
            Some(Value::String(id)) if !id.is_empty() => return id.clone(),
            Some(value) if !is_empty(value) => {
                return value.to_string().trim_matches('"').to_string()
            }
            _ => {}
        }
    }
    String::new()
}

/// Normalizes a gateway's method name to one of the record's `paymentMethod`
/// values, or `None` when it maps to none (Go's `normalizePaymentMethod`).
fn normalize_payment_method(method: &str) -> Option<&'static str> {
    let m = method.to_lowercase();
    if m.is_empty() {
        None
    } else if m.contains("paypal") {
        Some("paypal")
    } else if m.contains("crypto") {
        Some("crypto")
    } else if m.contains("card") || m.contains("visa") || m.contains("mastercard") {
        Some("card")
    } else if [
        "mobile", "momo", "mpesa", "m-pesa", "ussd", "airtel", "tigo", "halo",
    ]
    .iter()
    .any(|k| m.contains(k))
    {
        Some("mobile_money")
    } else if m.contains("bank") || m.contains("transfer") {
        Some("bank_transfer")
    } else {
        None
    }
}

/// A record field as a string (empty when missing or not a string).
fn string_of(record: &DataMap, key: &str) -> String {
    record
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}
