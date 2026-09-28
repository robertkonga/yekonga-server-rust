//! The request handle passed to handlers and middleware (port of
//! `yekonga/request.go`).
//!
//! [`Request`] is a cheap, clonable handle: handlers and middleware receive
//! their own clone and share the same underlying request, so values one
//! middleware stores with [`Request::set_context`] are visible to everything
//! that runs after it.

use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::{Arc, RwLock};

use bytes::Bytes;
use http::{HeaderMap, Method, Uri, Version};
use serde_json::{Map, Value};

use crate::app::Yekonga;
use crate::payload::{AuthPayload, ClientPayload, TokenPayload};

/// Context keys the built-in middleware store values under.
pub mod keys {
    pub const ACCESS_TOKEN: &str = "access_token";
    pub const REFRESH_TOKEN: &str = "refresh_token";
    pub const USER_INFO_PAYLOAD: &str = "userInfoPayload";
    pub const MASTER_KEY: &str = "masterKey";
    pub const CURRENT_TENANT_ID: &str = "currentTenantId";
}

#[derive(Clone)]
pub struct Request(Arc<Inner>);

struct Inner {
    app: Yekonga,
    method: Method,
    uri: Uri,
    version: Version,
    headers: HeaderMap,
    /// Percent-decoded URL path.
    path: String,
    query: Vec<(String, String)>,
    raw_body: Bytes,
    body: Value,
    form: Vec<(String, String)>,
    params: HashMap<String, String>,
    peer: Option<SocketAddr>,
    state: RwLock<State>,
}

#[derive(Default)]
struct State {
    context: Map<String, Value>,
    client: Option<ClientPayload>,
    token_payload: Option<TokenPayload>,
}

/// Everything needed to build a [`Request`]; filled in by the dispatcher.
pub(crate) struct RequestParts {
    pub app: Yekonga,
    pub method: Method,
    pub uri: Uri,
    pub version: Version,
    pub headers: HeaderMap,
    pub path: String,
    pub raw_body: Bytes,
    pub params: HashMap<String, String>,
    pub peer: Option<SocketAddr>,
}

impl Request {
    pub(crate) fn new(parts: RequestParts) -> Self {
        let query = parts
            .uri
            .query()
            .map(|q| form_urlencoded::parse(q.as_bytes()).into_owned().collect())
            .unwrap_or_default();

        let is_form = header_value(&parts.headers, "content-type")
            .starts_with("application/x-www-form-urlencoded");
        let form = if is_form {
            form_urlencoded::parse(&parts.raw_body)
                .into_owned()
                .collect()
        } else {
            Vec::new()
        };

        // Like the Go server, the body is decoded as JSON whatever its content
        // type; anything that isn't JSON leaves it null.
        let body = serde_json::from_slice(&parts.raw_body).unwrap_or(Value::Null);

        Self(Arc::new(Inner {
            app: parts.app,
            method: parts.method,
            uri: parts.uri,
            version: parts.version,
            headers: parts.headers,
            path: parts.path,
            query,
            raw_body: parts.raw_body,
            body,
            form,
            params: parts.params,
            peer: parts.peer,
            state: RwLock::new(State::default()),
        }))
    }

    pub fn app(&self) -> &Yekonga {
        &self.0.app
    }

    pub fn method(&self) -> &Method {
        &self.0.method
    }

    pub fn uri(&self) -> &Uri {
        &self.0.uri
    }

    pub fn version(&self) -> Version {
        self.0.version
    }

    /// The percent-decoded URL path.
    pub fn path(&self) -> &str {
        &self.0.path
    }

    /// `Host` header (or the URI authority for HTTP/2).
    pub fn host(&self) -> String {
        match self.header("host") {
            host if !host.is_empty() => host,
            _ => self
                .0
                .uri
                .authority()
                .map(|a| a.to_string())
                .unwrap_or_default(),
        }
    }

    pub fn peer_addr(&self) -> Option<SocketAddr> {
        self.0.peer
    }

    pub fn headers(&self) -> &HeaderMap {
        &self.0.headers
    }

    /// A header value, or an empty string.
    pub fn header(&self, name: &str) -> String {
        header_value(&self.0.headers, name).to_string()
    }

    /// A cookie value from the `Cookie` header.
    pub fn cookie(&self, name: &str) -> Option<String> {
        self.0
            .headers
            .get_all(http::header::COOKIE)
            .iter()
            .filter_map(|v| v.to_str().ok())
            .flat_map(|v| v.split(';'))
            .filter_map(|pair| pair.trim().split_once('='))
            .find(|(k, _)| *k == name)
            .map(|(_, v)| v.trim_matches('"').to_string())
    }

    /// A route parameter (`:name` in the route pattern), or an empty string.
    pub fn param(&self, name: &str) -> String {
        self.0.params.get(name).cloned().unwrap_or_default()
    }

    pub fn params(&self) -> &HashMap<String, String> {
        &self.0.params
    }

    /// First value of a query parameter, or an empty string.
    pub fn query(&self, key: &str) -> String {
        self.0
            .query
            .iter()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.clone())
            .unwrap_or_default()
    }

    pub fn query_int(&self, key: &str, default: i64) -> i64 {
        self.query(key).parse().unwrap_or(default)
    }

    pub fn query_float(&self, key: &str, default: f64) -> f64 {
        self.query(key).parse().unwrap_or(default)
    }

    /// Accepts the values Go's `strconv.ParseBool` does.
    pub fn query_bool(&self, key: &str, default: bool) -> bool {
        match self.query(key).as_str() {
            "1" | "t" | "T" | "TRUE" | "true" | "True" => true,
            "0" | "f" | "F" | "FALSE" | "false" | "False" => false,
            _ => default,
        }
    }

    /// Every value of a query parameter.
    pub fn query_array(&self, key: &str) -> Vec<String> {
        self.0
            .query
            .iter()
            .filter(|(k, _)| k == key)
            .map(|(_, v)| v.clone())
            .collect()
    }

    pub fn query_map(&self) -> HashMap<String, Vec<String>> {
        let mut map: HashMap<String, Vec<String>> = HashMap::new();
        for (k, v) in &self.0.query {
            map.entry(k.clone()).or_default().push(v.clone());
        }
        map
    }

    /// The request body decoded as JSON (`Null` if it isn't JSON).
    pub fn body(&self) -> &Value {
        &self.0.body
    }

    pub fn raw_body(&self) -> &Bytes {
        &self.0.raw_body
    }

    /// A field of a URL-encoded form body, or an empty string.
    pub fn form_value(&self, key: &str) -> String {
        self.0
            .form
            .iter()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.clone())
            .unwrap_or_default()
    }

    /// Whether the client sent or asked for JSON.
    pub fn wants_json(&self) -> bool {
        self.header("content-type").contains("json") || self.header("accept").contains("json")
    }

    /// Stores a value for later middleware and the handler.
    pub fn set_context(&self, key: impl Into<String>, value: impl Into<Value>) {
        self.write().context.insert(key.into(), value.into());
    }

    pub fn get_context(&self, key: &str) -> Option<Value> {
        self.read().context.get(key).cloned()
    }

    /// `get_context` for string values; empty if missing or not a string.
    pub fn context_string(&self, key: &str) -> String {
        match self.get_context(key) {
            Some(Value::String(s)) => s,
            _ => String::new(),
        }
    }

    pub fn client(&self) -> Option<ClientPayload> {
        self.read().client.clone()
    }

    pub fn set_client(&self, client: ClientPayload) {
        self.write().client = Some(client);
    }

    /// Claims of a valid access token, if the request carried one.
    pub fn token_payload(&self) -> Option<TokenPayload> {
        self.read().token_payload.clone()
    }

    pub fn set_token_payload(&self, payload: TokenPayload) {
        self.write().token_payload = Some(payload);
    }

    /// The user info stored by the user-info middleware.
    pub fn user_info(&self) -> Option<Map<String, Value>> {
        match self.get_context(keys::USER_INFO_PAYLOAD) {
            Some(Value::Object(map)) => Some(map),
            _ => None,
        }
    }

    /// The signed-in user, if any.
    pub fn auth(&self) -> Option<AuthPayload> {
        let info = self.read().context.get(keys::USER_INFO_PAYLOAD)?.clone();
        serde_json::from_value(info).ok()
    }

    pub fn tenant_id(&self) -> Option<Value> {
        self.get_context(keys::CURRENT_TENANT_ID)
            .filter(|v| !is_empty(v))
    }

    pub fn set_tenant_id(&self, tenant_id: impl Into<Value>) {
        self.set_context(keys::CURRENT_TENANT_ID, tenant_id);
    }

    /// The access token: from the token middleware, else the
    /// `Authorization` header, `token` query parameter or
    /// `X-Core-Session-Token` header. A `Bearer ` prefix is removed.
    pub fn token(&self) -> String {
        let stored = self.context_string(keys::ACCESS_TOKEN);
        if !stored.is_empty() {
            return stored;
        }

        let token = [
            self.header("authorization"),
            self.query("token"),
            self.header("x-core-session-token"),
        ]
        .into_iter()
        .find(|t| !t.is_empty())
        .unwrap_or_default();

        match bearer_token(&token) {
            Some(bearer) => bearer.to_string(),
            None => token.trim().to_string(),
        }
    }

    fn read(&self) -> std::sync::RwLockReadGuard<'_, State> {
        self.0.state.read().unwrap_or_else(|e| e.into_inner())
    }

    fn write(&self) -> std::sync::RwLockWriteGuard<'_, State> {
        self.0.state.write().unwrap_or_else(|e| e.into_inner())
    }
}

/// The token from a `Bearer <token>` value; the scheme is case-insensitive
/// (RFC 6750).
pub fn bearer_token(value: &str) -> Option<&str> {
    let value = value.trim();
    let prefix = value.get(..7)?;

    prefix
        .eq_ignore_ascii_case("bearer ")
        .then(|| value[7..].trim())
}

pub(crate) fn header_value<'a>(headers: &'a HeaderMap, name: &str) -> &'a str {
    headers
        .get(name)
        .and_then(|v| v.to_str().ok())
        .unwrap_or_default()
}

/// Go's `helper.IsEmpty` for JSON values.
pub(crate) fn is_empty(value: &Value) -> bool {
    match value {
        Value::Null => true,
        Value::String(s) => s.is_empty(),
        Value::Array(a) => a.is_empty(),
        Value::Object(o) => o.is_empty(),
        Value::Bool(b) => !b,
        Value::Number(n) => n.as_f64() == Some(0.0),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bearer() {
        assert_eq!(bearer_token("Bearer abc"), Some("abc"));
        assert_eq!(bearer_token("  bearer   abc "), Some("abc"));
        assert_eq!(bearer_token("Basic abc"), None);
        assert_eq!(bearer_token("abc"), None);
        assert_eq!(bearer_token("Bearér x"), None);
    }
}
