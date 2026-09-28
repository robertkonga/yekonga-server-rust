//! Built-in middleware (port of `yekonga/middleware.go`) and the types for
//! user middleware.
//!
//! Every request runs this chain before its route handler; the first error
//! aborts the request with its status code:
//!
//! 1. [`master_key`], [`application_id`]
//! 2. user [`MiddlewareKind::Preload`] middleware
//! 3. [`client`], [`tenant_catch`], [`token`], [`billing`], [`user_info`]
//! 4. user [`MiddlewareKind::Init`] middleware
//! 5. user [`MiddlewareKind::Global`] middleware
//! 6. the route handler, or when no route matched, [`MiddlewareKind::Catch`]
//!    middleware until one responds, then the default 404 page.

use serde_json::{json, Value};

use crate::db::values::is_empty;
use crate::helper::{
    extract_domain, get_base_url, get_client_ip, get_main_domain, jwt, match_path,
};
use crate::payload::{ClientPayload, TokenPayload};
use crate::request::{bearer_token, keys, Request};
use crate::response::Response;

/// Aborts the request with an HTTP status and message. For 307/308 the
/// message is the redirect URL.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Abort {
    pub status: u16,
    pub message: String,
}

impl Abort {
    pub fn new(status: u16, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
        }
    }
}

impl std::fmt::Display for Abort {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{} {}", self.status, self.message)
    }
}

impl std::error::Error for Abort {}

pub type MiddlewareResult = Result<(), Abort>;

/// Where a user middleware runs in the chain (see the module docs).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MiddlewareKind {
    /// After the built-in auth pipeline, right before the route handler.
    Global,
    /// After the built-in auth pipeline, before global middleware.
    Init,
    /// Before the built-in client/tenant/token pipeline.
    Preload,
    /// Only when no route matched; the 404 fallback chain.
    Catch,
}

/// Extensions exempt from the application key check.
const APP_KEY_EXEMPT_EXTENSIONS: &[&str] = &[
    ".css", ".js", ".ico", ".pdf", ".flv", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".woff2",
    ".woff", ".ttf", ".eot",
];

/// Checks a `master-key` / `X-Core-Master-Key` header or `master-key` query
/// parameter against the config, and records it on the request.
pub fn master_key(req: &Request) -> MiddlewareResult {
    let key = [
        req.header("master-key"),
        req.header("x-core-master-key"),
        req.query("master-key"),
    ]
    .into_iter()
    .find(|k| !k.is_empty());

    if let Some(key) = key {
        req.set_context(keys::MASTER_KEY, key.clone());

        if key != req.app().config().master_key {
            return Err(Abort::new(401, "master key invalid"));
        }
    }

    Ok(())
}

/// When `enableAppKey` is on, requires the application key (header
/// `application-key` / `X-Core-Application-Id`, or query `application-key` /
/// `app-key`) except for static assets.
pub fn application_id(req: &Request) -> MiddlewareResult {
    let config = req.app().config();
    if !config.enable_app_key {
        return Ok(());
    }

    let extension = file_extension(req.path());
    if APP_KEY_EXEMPT_EXTENSIONS.contains(&extension.as_str()) {
        return Ok(());
    }

    let key = [
        req.header("application-key"),
        req.header("x-core-application-id"),
        req.query("application-key"),
        req.query("app-key"),
    ]
    .into_iter()
    .find(|k| !k.is_empty());

    match key {
        Some(key) if key == config.app_key => Ok(()),
        Some(_) => Err(Abort::new(401, "application key invalid")),
        None => Err(Abort::new(401, "application key not provided")),
    }
}

/// Records where the request came from ([`Request::client`]).
pub fn client(req: &Request) -> MiddlewareResult {
    let host_header = req.host().to_lowercase();
    let mut host_parts = host_header.split(':');
    let host = host_parts.next().unwrap_or_default().to_string();
    let port = host_parts.next_back().unwrap_or_default().to_string();

    let forwarded_proto = req.header("x-forwarded-proto");
    // Go reports the protocol name from the request line ("HTTP/1.1" -> "http").
    let proto = if forwarded_proto.is_empty() {
        "http".to_string()
    } else {
        forwarded_proto
    };

    let mut origin = req.header("origin");
    if origin.is_empty() {
        origin = extract_domain(&req.header("referer"));
        if origin.is_empty() {
            origin = host.clone();
        }
    }
    let origin = format!("{proto}://{}", extract_domain(&origin));

    req.set_client(ClientPayload {
        tenant_id: Value::Null,
        origin,
        host,
        port,
        proto,
        path: req.path().to_string(),
        method: req.method().as_str().to_lowercase(),
        user_agent: req.header("user-agent"),
        ip_address: get_client_ip(req.headers(), req.peer_addr().map(|a| a.ip())),
    });

    Ok(())
}

/// Resolves the tenant from the request's domain.
///
/// With `hasTenant`, the Tenant whose domain, subdomain, custom domain or
/// custom subdomain is the origin's host; its TenantConfig record is
/// available as [`Request::tenant_config`]. With `hasTenantCatch`, the
/// TenantCatch record for the host. A subdomain of the main domain without a
/// tenant is rejected, and with `tenantOnly` so is any request without one.
///
/// A tenant id set by a preload middleware is kept when the lookup finds
/// nothing. (The Go version's `FetchTenantByDomain` cloud-function fallback
/// arrives with cloud functions.)
pub async fn tenant_catch(req: &Request) -> MiddlewareResult {
    let app = req.app();
    let config = app.config();
    if !(config.has_tenant || config.has_tenant_catch) {
        return Ok(());
    }

    let host = req.client().map(|c| c.origin_domain()).unwrap_or_default();
    let mut tenant_id: Option<Value> = None;

    if config.has_tenant {
        let records = app.tenant_by_host(&host).await;
        tenant_id = records
            .tenant
            .as_ref()
            .and_then(|t| t.get("_id"))
            .cloned()
            .filter(|v| !is_empty(v));

        if tenant_id.is_some() {
            let tenant_config = records.config.unwrap_or_else(|| {
                let mut config = serde_json::Map::new();
                config.insert("tenantId".into(), tenant_id.clone().unwrap_or(Value::Null));
                config
            });
            req.set_context(keys::CURRENT_TENANT_CONFIG, Value::Object(tenant_config));
        }
    } else if config.has_tenant_catch {
        if let Some(result) = app.fetch_tenant_by_domain(&host, req).await {
            tenant_id = result.get("tenantId").cloned().filter(|v| !is_empty(v));
        }
    }

    let tenant_id = tenant_id.or_else(|| req.tenant_id());

    if config.has_tenant && config.tenant_only && tenant_id.is_none() {
        return Err(Abort::new(400, "Tenant not found for the request"));
    }

    if let Some(main_domain) = get_main_domain(&host) {
        if host != main_domain && tenant_id.is_none() {
            return Err(Abort::new(404, "Tenant not found"));
        }
    }

    if let Some(tenant_id) = tenant_id {
        req.set_tenant_id(tenant_id.clone());
        if let Some(mut client) = req.client() {
            client.tenant_id = tenant_id;
            req.set_client(client);
        }
    }

    Ok(())
}

/// Validates the access token (cookie `access_token` or `Authorization:
/// Bearer`) and stores its claims ([`Request::token_payload`]).
///
/// A missing or bad token is only an error on routes that need one: every
/// route except the framework's own auth/config/API endpoints and the
/// app's public routes.
pub fn token(req: &Request) -> MiddlewareResult {
    let app = req.app();
    let config = app.config();
    let module_name = req.param("moduleName");
    let master_key = req.context_string(keys::MASTER_KEY);
    let domain = req.client().map(|c| c.origin_domain()).unwrap_or_default();
    let is_json = req.wants_json();
    let current_path = req.path();

    let mut mandatory = !app.is_token_optional_path(current_path);
    if mandatory
        && app
            .public_routes()
            .iter()
            .any(|r| match_path(current_path, &app.append_base_url(r)))
    {
        mandatory = false;
    }

    let access_token = req
        .cookie(keys::ACCESS_TOKEN)
        .filter(|t| !t.is_empty())
        .or_else(|| bearer_token(&req.header("authorization")).map(String::from))
        .unwrap_or_default();
    let refresh_token = req.cookie(keys::REFRESH_TOKEN).unwrap_or_default();

    let base_url =
        |path: &str| get_base_url(path, &domain, &config.base_url, config.ports.server as u16);
    let logout_url = if module_name.is_empty() {
        "logout".to_string()
    } else {
        format!("logout/{module_name}")
    };

    if !access_token.is_empty() {
        let payload =
            jwt::decode_into::<TokenPayload>(&access_token, &config.authentication.secret_token)
                .map(|(_, payload)| payload);

        match &payload {
            None if mandatory => {
                return Err(if is_json {
                    Abort::new(307, "Access token invalid")
                } else {
                    Abort::new(307, base_url(&logout_url))
                });
            }
            Some(payload) if payload.is_expired() && mandatory => {
                return Err(if is_json {
                    Abort::new(401, "Token expired")
                } else {
                    Abort::new(
                        307,
                        format!("{}?redirect={current_path}", base_url("refresh")),
                    )
                });
            }
            _ => {}
        }

        if let Some(payload) = payload {
            if !payload.domain.is_empty() && payload.domain != domain {
                return Err(if is_json {
                    Abort::new(401, "Domain mismatch expired")
                } else {
                    Abort::new(307, base_url(&logout_url))
                });
            }

            if config.tenant_only && is_empty(&payload.tenant_id) {
                return Err(Abort::new(400, "tenant not found for the request"));
            }

            if !is_empty(&payload.tenant_id) && req.tenant_id().is_none() {
                req.set_tenant_id(payload.tenant_id.clone());
            }

            req.set_token_payload(payload);
        }

        req.set_context(keys::ACCESS_TOKEN, access_token.clone());
    }

    if !refresh_token.is_empty() {
        req.set_context(keys::REFRESH_TOKEN, refresh_token);
    }

    let has_master_key = !master_key.is_empty() && master_key == config.master_key;
    if config.authorized_only && !has_master_key && mandatory && access_token.is_empty() && is_json
    {
        return Err(Abort::new(401, "Must be authorized/login"));
    }

    Ok(())
}

/// Tenant billing checks (a no-op in the Go server too).
pub fn billing(_req: &Request) -> MiddlewareResult {
    Ok(())
}

/// Stores the signed-in user's info ([`Request::user_info`] / [`Request::auth`]).
/// An authorization server loads the user's record; other servers use the
/// access token's claims.
pub async fn user_info(req: &Request) -> MiddlewareResult {
    let Some(payload) = req.token_payload() else {
        return Ok(());
    };
    if payload.user_id.is_empty() {
        return Ok(());
    }

    let info = if req.app().config().is_authorization_server {
        req.app().user_by_id(&payload.user_id).await
    } else {
        let mut data = payload.to_map();
        data.insert("_id".into(), json!(payload.user_id));
        data.insert("id".into(), json!(payload.user_id));
        Some(data)
    };

    if let Some(info) = info {
        req.set_context(keys::USER_INFO_PAYLOAD, Value::Object(info));
    }

    Ok(())
}

/// The built-in steps that run before preload middleware, in order.
pub(crate) fn run_builtin_head(req: &Request, _res: &Response) -> MiddlewareResult {
    master_key(req)?;
    application_id(req)
}

/// The built-in steps between preload and init middleware, in order.
pub(crate) async fn run_builtin_auth(req: &Request, _res: &Response) -> MiddlewareResult {
    client(req)?;
    tenant_catch(req).await?;
    token(req)?;
    billing(req)?;
    user_info(req).await
}

/// Go's `filepath.Ext`: the suffix from the last dot in the final element.
pub(crate) fn file_extension(path: &str) -> String {
    let name = path.rsplit('/').next().unwrap_or_default();
    match name.rfind('.') {
        Some(i) => name[i..].to_string(),
        None => String::new(),
    }
}
