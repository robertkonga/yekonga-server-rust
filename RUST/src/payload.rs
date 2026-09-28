//! Per-request identity payloads (port of the payload types in
//! `yekonga/request.go`). Serialized names match the Go JSON tags, so tokens
//! and user info exchanged with Go services decode the same way.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::helper::extract_domain;
use crate::helper::lenient::or_default;

/// Where the request came from (set by the client middleware).
#[derive(Clone, Debug, Default, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ClientPayload {
    pub tenant_id: Value,
    pub origin: String,
    pub host: String,
    pub port: String,
    #[serde(rename = "protocol")]
    pub proto: String,
    pub path: String,
    pub method: String,
    pub user_agent: String,
    pub ip_address: String,
}

impl ClientPayload {
    /// `host[:port]` of the request's origin.
    pub fn origin_domain(&self) -> String {
        extract_domain(&self.origin)
    }
}

/// Claims carried in an access token.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TokenPayload {
    #[serde(default, deserialize_with = "or_default")]
    pub domain: String,
    #[serde(default, deserialize_with = "or_default")]
    pub tenant_id: Value,
    #[serde(default, deserialize_with = "or_default")]
    pub profile_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub user_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub admin_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub module_name: String,
    #[serde(default, deserialize_with = "or_default")]
    pub username_type: String,
    #[serde(default, deserialize_with = "or_default")]
    pub username: String,
    #[serde(default, deserialize_with = "or_default")]
    pub phone: String,
    #[serde(default, deserialize_with = "or_default")]
    pub email: String,
    #[serde(default, deserialize_with = "or_default")]
    pub whatsapp: String,
    #[serde(default, deserialize_with = "or_default")]
    pub roles: Vec<String>,
    #[serde(default, deserialize_with = "or_default")]
    pub permissions: Vec<String>,
    /// `None` when absent, which counts as expired (Go's zero time).
    #[serde(default, deserialize_with = "or_default")]
    pub expires_at: Option<DateTime<Utc>>,
}

impl TokenPayload {
    pub fn is_expired(&self) -> bool {
        self.expires_at.is_none_or(|at| at < Utc::now())
    }

    pub fn to_map(&self) -> Map<String, Value> {
        match serde_json::to_value(self) {
            Ok(Value::Object(map)) => map,
            _ => Map::new(),
        }
    }
}

/// The signed-in user, decoded from the user info the middleware stored.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AuthPayload {
    #[serde(default, deserialize_with = "or_default")]
    pub id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub domain: String,
    #[serde(default, deserialize_with = "or_default")]
    pub tenant_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub profile_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub user_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub admin_id: String,
    #[serde(default, deserialize_with = "or_default")]
    pub module_name: String,
    #[serde(default, deserialize_with = "or_default")]
    pub username_type: String,
    #[serde(default, deserialize_with = "or_default")]
    pub username: String,
    #[serde(default, deserialize_with = "or_default")]
    pub first_name: String,
    #[serde(default, deserialize_with = "or_default")]
    pub last_name: String,
    #[serde(default, deserialize_with = "or_default")]
    pub phone: String,
    #[serde(default, deserialize_with = "or_default")]
    pub email: String,
    #[serde(default, deserialize_with = "or_default")]
    pub whatsapp: String,
    #[serde(default, deserialize_with = "or_default")]
    pub roles: Vec<String>,
    #[serde(default, deserialize_with = "or_default")]
    pub permissions: Vec<String>,
    #[serde(default, deserialize_with = "or_default")]
    pub expires_at: Option<DateTime<Utc>>,
    #[serde(default, deserialize_with = "or_default")]
    pub extracts: Map<String, Value>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn token_payload_is_lenient() {
        let payload: TokenPayload = serde_json::from_value(json!({
            "userId": "u1", "tenantId": 5, "roles": null, "expiresAt": "2999-01-01T00:00:00Z", "exp": 1
        }))
        .unwrap();

        assert_eq!(payload.user_id, "u1");
        assert_eq!(payload.tenant_id, json!(5));
        assert!(payload.roles.is_empty());
        assert!(!payload.is_expired());
        assert!(TokenPayload::default().is_expired());
    }

    #[test]
    fn auth_from_user_info() {
        let auth: AuthPayload = serde_json::from_value(
            json!({"id": "u1", "tenantId": 7, "email": "a@b.tz", "extracts": null}),
        )
        .unwrap();

        assert_eq!(auth.id, "u1");
        assert_eq!(auth.tenant_id, "7");
        assert_eq!(auth.email, "a@b.tz");
    }
}
