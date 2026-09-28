//! Rate limiting, the error guard and the IP whitelist over HTTP.

use std::sync::Arc;

use axum::body::Body;
use http::Request as HttpRequest;
use serde_json::{json, Value};
use tower::ServiceExt;
use yekonga::{DatabaseStructure, LocalBackend, Yekonga, YekongaConfig};

fn app_with(config: Value) -> Yekonga {
    let config: YekongaConfig = serde_json::from_value(config).unwrap();
    Yekonga::with_backend(
        config,
        DatabaseStructure::from_value(&json!({})),
        Arc::new(LocalBackend::in_memory()),
    )
}

/// Sends a GET as the given client IP (via X-Forwarded-For, which the test
/// configs trust) and returns the status.
async fn status(app: &Yekonga, ip: &str, uri: &str) -> u16 {
    let request = HttpRequest::builder()
        .method("GET")
        .uri(uri)
        .header("host", "shop.tz")
        .header("x-forwarded-for", ip)
        .body(Body::empty())
        .unwrap();
    app.router()
        .oneshot(request)
        .await
        .unwrap()
        .status()
        .as_u16()
}

#[tokio::test]
async fn rate_limiter_throttles_a_client() {
    let app = app_with(json!({
        "authentication": {"secretToken": "s"},
        "security": {"trustProxyHeaders": true, "rateLimit": {"enabled": true, "requestsPerMinute": 60, "burst": 3}}
    }));

    // The burst of 3 is allowed, then the client is throttled.
    for _ in 0..3 {
        assert_eq!(status(&app, "1.2.3.4", "/health").await, 200);
    }
    assert_eq!(status(&app, "1.2.3.4", "/health").await, 429);

    // A different client has its own bucket.
    assert_eq!(status(&app, "5.6.7.8", "/health").await, 200);
}

#[tokio::test]
async fn rate_limiter_off_by_default() {
    let app = app_with(json!({"authentication": {"secretToken": "s"}}));
    for _ in 0..10 {
        assert_eq!(status(&app, "1.2.3.4", "/health").await, 200);
    }
}

#[tokio::test]
async fn error_guard_blocks_after_a_flood_of_errors() {
    let app = app_with(json!({
        "authentication": {"secretToken": "s"},
        "security": {"trustProxyHeaders": true, "errorGuard": {"enabled": true, "requestsPerSecond": 3}}
    }));

    // 404s count as errors. Four in the same second trip the block; from then
    // on every request (even to a valid path) is refused with 403.
    for _ in 0..4 {
        assert_eq!(status(&app, "9.9.9.9", "/no-such-path").await, 404);
    }
    assert_eq!(status(&app, "9.9.9.9", "/health").await, 403);

    // A different client is unaffected.
    assert_eq!(status(&app, "8.8.8.8", "/health").await, 200);

    // The block was persisted to IpAccessRule.
    let rule = loop {
        let found = app
            .query("IpAccessRule")
            .unwrap()
            .where_("ipAddress", "9.9.9.9")
            .find_one()
            .await
            .unwrap();
        if let Some(rule) = found {
            break rule;
        }
        tokio::time::sleep(std::time::Duration::from_millis(20)).await;
    };
    assert_eq!(rule["type"], "blacklist");
    assert_eq!(rule["source"], "errorGuard");
}

#[tokio::test]
async fn whitelist_exempts_a_client() {
    let app = app_with(json!({
        "authentication": {"secretToken": "s"},
        "security": {"trustProxyHeaders": true, "rateLimit": {"enabled": true, "requestsPerMinute": 60, "burst": 1}}
    }));
    app.query("IpAccessRule")
        .unwrap()
        .create(json!({"ipAddress": "1.2.3.4", "type": "whitelist"}))
        .await
        .unwrap();

    // The whitelisted client is never throttled, however many requests.
    for _ in 0..5 {
        assert_eq!(status(&app, "1.2.3.4", "/health").await, 200);
    }
    // A non-whitelisted client still hits the burst-1 limit.
    assert_eq!(status(&app, "2.2.2.2", "/health").await, 200);
    assert_eq!(status(&app, "2.2.2.2", "/health").await, 429);
}

#[tokio::test]
async fn preexisting_blacklist_row_blocks() {
    let app = app_with(json!({
        "authentication": {"secretToken": "s"},
        "security": {"trustProxyHeaders": true, "errorGuard": {"enabled": true}}
    }));
    app.query("IpAccessRule")
        .unwrap()
        .create(json!({"ipAddress": "6.6.6.6", "type": "blacklist"}))
        .await
        .unwrap();

    // The guard loads existing blacklist rows on first use.
    assert_eq!(status(&app, "6.6.6.6", "/health").await, 403);
    assert_eq!(status(&app, "7.7.7.7", "/health").await, 200);
}
