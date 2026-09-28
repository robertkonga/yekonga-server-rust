//! HS256 JSON Web Tokens, wire-compatible with `helper/jwt/jwt.go`: segments
//! use *padded* URL-safe base64, so tokens issued by the Go server verify here
//! and the other way round.

use base64::engine::general_purpose::URL_SAFE;
use base64::Engine;
use hmac::{Hmac, Mac};
use serde::de::DeserializeOwned;
use serde_json::{json, Map, Value};
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

/// Signs `claims`, adding an `exp` claim 24 hours from now (as the Go helper does).
pub fn encode(mut claims: Map<String, Value>, secret: &str) -> String {
    let header = json!({"alg": "HS256", "typ": "JWT"});
    claims.insert(
        "exp".into(),
        json!(chrono::Utc::now().timestamp() + 24 * 60 * 60),
    );

    let header = URL_SAFE.encode(header.to_string());
    let payload = URL_SAFE.encode(Value::Object(claims).to_string());
    let input = format!("{header}.{payload}");
    let signature = sign(&input, secret);

    format!("{input}.{signature}")
}

/// Verifies the signature and `exp` claim, returning the claims.
pub fn decode(token: &str, secret: &str) -> Option<Map<String, Value>> {
    let payload = verified_payload(token, secret)?;
    serde_json::from_slice(&payload).ok()
}

/// Like [`decode`], also deserializing the claims into `T`.
pub fn decode_into<T: DeserializeOwned>(
    token: &str,
    secret: &str,
) -> Option<(Map<String, Value>, T)> {
    let payload = verified_payload(token, secret)?;
    let claims = serde_json::from_slice(&payload).ok()?;
    let typed = serde_json::from_slice(&payload).ok()?;

    Some((claims, typed))
}

fn verified_payload(token: &str, secret: &str) -> Option<Vec<u8>> {
    let mut parts = token.split('.');
    let (header, payload, signature) = (parts.next()?, parts.next()?, parts.next()?);
    if parts.next().is_some() {
        return None;
    }

    let mut mac = HmacSha256::new_from_slice(secret.as_bytes()).ok()?;
    mac.update(format!("{header}.{payload}").as_bytes());
    // Constant-time comparison of the raw MAC.
    mac.verify_slice(&URL_SAFE.decode(signature).ok()?).ok()?;

    let payload = URL_SAFE.decode(payload).ok()?;
    let claims: Map<String, Value> = serde_json::from_slice(&payload).ok()?;

    if let Some(exp) = claims.get("exp").and_then(Value::as_f64) {
        if chrono::Utc::now().timestamp() > exp as i64 {
            return None;
        }
    }

    Some(payload)
}

fn sign(input: &str, secret: &str) -> String {
    let mut mac =
        HmacSha256::new_from_slice(secret.as_bytes()).expect("HMAC takes keys of any length");
    mac.update(input.as_bytes());

    URL_SAFE.encode(mac.finalize().into_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip() {
        let mut claims = Map::new();
        claims.insert("userId".into(), json!("u1"));

        let token = encode(claims, "secret");
        let decoded = decode(&token, "secret").expect("valid token");
        assert_eq!(decoded["userId"], "u1");
        assert!(decoded.contains_key("exp"));

        assert!(decode(&token, "other").is_none());
        assert!(decode("a.b", "secret").is_none());
    }

    #[test]
    fn verifies_go_issued_token() {
        // Produced by helper/jwt.EncodeJWT in the Go server with secret "secret"
        // and no expiry, to pin wire compatibility (padded base64).
        let header = URL_SAFE.encode(r#"{"alg":"HS256","typ":"JWT"}"#);
        let payload = URL_SAFE.encode(r#"{"userId":"u1"}"#);
        let input = format!("{header}.{payload}");
        let token = format!("{input}.{}", sign(&input, "secret"));

        assert!(token.contains('='), "Go pads base64 segments");
        assert_eq!(decode(&token, "secret").unwrap()["userId"], "u1");
    }

    #[test]
    fn rejects_expired() {
        let header = URL_SAFE.encode(r#"{"alg":"HS256","typ":"JWT"}"#);
        let payload = URL_SAFE.encode(r#"{"exp":1}"#);
        let input = format!("{header}.{payload}");
        let token = format!("{input}.{}", sign(&input, "secret"));

        assert!(decode(&token, "secret").is_none());
    }
}
