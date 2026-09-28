//! Abuse protection (ports of `yekonga/rate_limit.go`, `error_guard.go` and
//! `ip_whitelist.go`): a per-client rate limiter, an error guard that blocks
//! clients flooding the server with error responses, and a shared IP
//! whitelist that exempts clients from both. Blocks and exemptions are backed
//! by the `IpAccessRule` collection.

use std::collections::{HashMap, HashSet};
use std::sync::{Mutex, OnceLock, RwLock};
use std::time::{Duration, Instant};

use chrono::Utc;
use http::HeaderMap;
use serde_json::{json, Value};

use crate::app::Yekonga;
use crate::db::values::parse_datetime;
use crate::request::{header_value, Request};

const IP_ACCESS_RULE_MODEL: &str = "IpAccessRule";
const REFRESH_INTERVAL: Duration = Duration::from_secs(30);
const DEFAULT_STATUS_CODES: &[u16] = &[400, 401, 403, 404];

/// The abuse-protection state, built lazily on first use.
#[derive(Default)]
pub(crate) struct Security {
    rate_limiter: OnceLock<RateLimiterStore>,
    error_guard: OnceLock<ErrorGuard>,
    ip_whitelist: OnceLock<IpWhitelist>,
}

// ----- rate limiter ----------------------------------------------------------------------

/// A per-client token bucket: starts with `burst` tokens and refills at
/// `refill_per_sec`, capped at `burst`; each allowed request costs one.
struct TokenBucket {
    tokens: f64,
    burst: f64,
    refill_per_sec: f64,
    last_seen: Instant,
}

impl TokenBucket {
    fn new(requests_per_minute: f64, burst: f64) -> Self {
        Self {
            tokens: burst,
            burst,
            refill_per_sec: requests_per_minute / 60.0,
            last_seen: Instant::now(),
        }
    }

    fn allow(&mut self) -> bool {
        let now = Instant::now();
        let elapsed = now.duration_since(self.last_seen).as_secs_f64();
        self.last_seen = now;

        self.tokens = (self.tokens + elapsed * self.refill_per_sec).min(self.burst);
        if self.tokens < 1.0 {
            return false;
        }
        self.tokens -= 1.0;
        true
    }
}

struct RateLimiterStore {
    buckets: Mutex<HashMap<String, TokenBucket>>,
    requests_per_minute: f64,
    burst: f64,
}

impl RateLimiterStore {
    fn allow(&self, key: &str) -> bool {
        let mut buckets = self.buckets.lock().unwrap_or_else(|e| e.into_inner());
        buckets
            .entry(key.to_string())
            .or_insert_with(|| TokenBucket::new(self.requests_per_minute, self.burst))
            .allow()
    }
}

// ----- error guard -----------------------------------------------------------------------

/// A fixed one-second window counter (resets when the second changes).
#[derive(Default)]
struct ErrorCounter {
    second: i64,
    count: i64,
}

struct ErrorGuard {
    counters: Mutex<HashMap<String, ErrorCounter>>,
    /// Client key -> unblock time (`None` = forever).
    blocked: RwLock<HashMap<String, Option<chrono::DateTime<Utc>>>>,
    status_codes: HashSet<u16>,
    requests_per_second: i64,
    /// The block length; `None` blocks permanently.
    block_duration: Option<chrono::Duration>,
}

impl ErrorGuard {
    fn tracks(&self, status: u16) -> bool {
        self.status_codes.contains(&status)
    }

    fn is_blocked(&self, key: &str) -> bool {
        let until = {
            let blocked = self.blocked.read().unwrap_or_else(|e| e.into_inner());
            match blocked.get(key) {
                Some(until) => *until,
                None => return false,
            }
        };
        match until {
            Some(until) if Utc::now() > until => {
                self.blocked
                    .write()
                    .unwrap_or_else(|e| e.into_inner())
                    .remove(key);
                false
            }
            _ => true,
        }
    }

    /// Rebuilds the blocked set from the current blacklist rows.
    fn sync_from_rows(&self, rows: &[Value]) {
        let now = Utc::now();
        let mut blocked = HashMap::new();
        for row in rows {
            if row.get("type").and_then(Value::as_str) != Some("blacklist") {
                continue;
            }
            let Some(ip) = row
                .get("ipAddress")
                .and_then(Value::as_str)
                .filter(|s| !s.is_empty())
            else {
                continue;
            };
            let until = match row
                .get("expiresAt")
                .and_then(Value::as_str)
                .and_then(parse_datetime)
            {
                Some(at) if at < now => continue, // already expired
                other => other,
            };
            blocked.insert(ip.to_string(), until);
        }
        *self.blocked.write().unwrap_or_else(|e| e.into_inner()) = blocked;
    }

    /// Registers one tracked error for `key`; returns true the moment it
    /// crosses the threshold into being blocked.
    fn record_error(&self, key: &str) -> bool {
        let second = Utc::now().timestamp();
        {
            let mut counters = self.counters.lock().unwrap_or_else(|e| e.into_inner());
            let counter = counters.entry(key.to_string()).or_default();
            if counter.second != second {
                counter.second = second;
                counter.count = 0;
            }
            counter.count += 1;
            if counter.count <= self.requests_per_second {
                return false;
            }
            counters.remove(key);
        }

        let mut blocked = self.blocked.write().unwrap_or_else(|e| e.into_inner());
        if blocked.contains_key(key) {
            return false;
        }
        let until = self.block_duration.map(|d| Utc::now() + d);
        blocked.insert(key.to_string(), until);
        true
    }
}

// ----- IP whitelist ----------------------------------------------------------------------

#[derive(Default)]
struct IpWhitelist {
    ips: RwLock<HashSet<String>>,
}

impl IpWhitelist {
    fn is_whitelisted(&self, key: &str) -> bool {
        self.ips
            .read()
            .unwrap_or_else(|e| e.into_inner())
            .contains(key)
    }

    fn sync_from_rows(&self, rows: &[Value]) {
        let ips = rows
            .iter()
            .filter(|r| r.get("type").and_then(Value::as_str) == Some("whitelist"))
            .filter_map(|r| r.get("ipAddress").and_then(Value::as_str))
            .filter(|s| !s.is_empty())
            .map(String::from)
            .collect();
        *self.ips.write().unwrap_or_else(|e| e.into_inner()) = ips;
    }
}

/// The request's client IP, used to key the guards. `X-Forwarded-For` and
/// `X-Real-Ip` are trusted only when `trust_proxy_headers` is set; otherwise
/// a client could forge them to dodge the limiter (Go's `clientIP`).
pub(crate) fn client_key(
    headers: &HeaderMap,
    peer: Option<std::net::SocketAddr>,
    trust_proxy_headers: bool,
) -> String {
    if trust_proxy_headers {
        let xff = header_value(headers, "x-forwarded-for");
        if !xff.is_empty() {
            return xff.split(',').next().unwrap_or(xff).trim().to_string();
        }
        let xrip = header_value(headers, "x-real-ip");
        if !xrip.is_empty() {
            return xrip.trim().to_string();
        }
    }
    peer.map(|p| p.ip().to_string()).unwrap_or_default()
}

impl Yekonga {
    /// Whether the client is exempt from the rate limiter and error guard.
    pub(crate) async fn is_ip_whitelisted(&self, key: &str) -> bool {
        let whitelist = self.ensure_ip_whitelist().await;
        whitelist.is_whitelisted(key)
    }

    async fn ensure_ip_whitelist(&self) -> &IpWhitelist {
        if let Some(whitelist) = self.security().ip_whitelist.get() {
            return whitelist;
        }
        // Load once now, then refresh in the background.
        let rows = self.ip_access_rules().await;
        let whitelist = IpWhitelist::default();
        whitelist.sync_from_rows(&rows);
        let _ = self.security().ip_whitelist.set(whitelist);

        let app = self.clone();
        tokio::spawn(async move {
            let mut ticker = tokio::time::interval(REFRESH_INTERVAL);
            ticker.tick().await;
            loop {
                ticker.tick().await;
                let rows = app.ip_access_rules().await;
                if let Some(whitelist) = app.security().ip_whitelist.get() {
                    whitelist.sync_from_rows(&rows);
                }
            }
        });
        self.security().ip_whitelist.get().unwrap()
    }

    /// Every `IpAccessRule` row, or empty when the model or backend is
    /// unavailable.
    async fn ip_access_rules(&self) -> Vec<Value> {
        match self.query(IP_ACCESS_RULE_MODEL) {
            Ok(query) => query
                .skip_before_commit()
                .find()
                .await
                .unwrap_or_default()
                .into_iter()
                .map(Value::Object)
                .collect(),
            Err(_) => Vec::new(),
        }
    }

    /// Whether the request may proceed past the rate limiter (Go's
    /// `checkRateLimit`). A blocked request should get a 429.
    pub(crate) async fn allow_rate_limit(&self, key: &str) -> bool {
        let config = &self.config().security.rate_limit;
        if !config.enabled {
            return true;
        }
        if self.is_ip_whitelisted(key).await {
            return true;
        }

        let store = self.security().rate_limiter.get_or_init(|| {
            let rpm = if config.requests_per_minute > 0 {
                config.requests_per_minute
            } else {
                300
            };
            let burst = if config.burst > 0 { config.burst } else { rpm };
            RateLimiterStore {
                buckets: Mutex::new(HashMap::new()),
                requests_per_minute: rpm as f64,
                burst: burst as f64,
            }
        });
        store.allow(key)
    }

    /// Whether the request may proceed past the error guard (Go's
    /// `checkErrorGuardBlock`). A blocked client should get a 403.
    pub(crate) async fn allow_error_guard(&self, key: &str) -> bool {
        if !self.config().security.error_guard.enabled {
            return true;
        }
        if self.is_ip_whitelisted(key).await {
            return true;
        }
        !self.ensure_error_guard().await.is_blocked(key)
    }

    async fn ensure_error_guard(&self) -> &ErrorGuard {
        if let Some(guard) = self.security().error_guard.get() {
            return guard;
        }
        let cfg = &self.config().security.error_guard;
        let status_codes: HashSet<u16> = if cfg.status_codes.is_empty() {
            DEFAULT_STATUS_CODES.iter().copied().collect()
        } else {
            cfg.status_codes.iter().copied().collect()
        };
        let requests_per_second = if cfg.requests_per_second > 0 {
            cfg.requests_per_second
        } else {
            20
        };
        let block_duration =
            (cfg.block_hours > 0).then(|| chrono::Duration::hours(cfg.block_hours));

        let guard = ErrorGuard {
            counters: Mutex::new(HashMap::new()),
            blocked: RwLock::new(HashMap::new()),
            status_codes,
            requests_per_second,
            block_duration,
        };
        guard.sync_from_rows(&self.ip_access_rules().await);
        let _ = self.security().error_guard.set(guard);

        let app = self.clone();
        tokio::spawn(async move {
            let mut ticker = tokio::time::interval(REFRESH_INTERVAL);
            ticker.tick().await;
            loop {
                ticker.tick().await;
                let rows = app.ip_access_rules().await;
                if let Some(guard) = app.security().error_guard.get() {
                    guard.sync_from_rows(&rows);
                }
            }
        });
        self.security().error_guard.get().unwrap()
    }

    /// Registers one response's status code against the request's client, and
    /// persists a blacklist row the first time that trips the guard (Go's
    /// `recordErrorResponse`).
    pub(crate) async fn record_error_response(&self, req: &Request, key: &str, status: u16) {
        if !self.config().security.error_guard.enabled {
            return;
        }
        if self.is_ip_whitelisted(key).await {
            return;
        }
        let guard = self.ensure_error_guard().await;
        if !guard.tracks(status) {
            return;
        }
        if guard.record_error(key) {
            self.persist_ip_block(req, key, status);
        }
    }

    /// Writes a blacklist `IpAccessRule` capturing the request that tripped
    /// the guard (Go's `persistIpBlock`), off the request path.
    fn persist_ip_block(&self, req: &Request, ip: &str, status: u16) {
        let cfg = &self.config().security.error_guard;
        let reason = format!(
            "exceeded {} error responses/second (tripped by {status})",
            cfg.requests_per_second
        );
        let headers: serde_json::Map<String, Value> = req
            .headers()
            .iter()
            .map(|(k, v)| (k.to_string(), json!(v.to_str().unwrap_or_default())))
            .collect();
        let data = json!({
            "ipAddress": ip,
            "type": "blacklist",
            "source": "errorGuard",
            "reason": reason,
            "url": req.uri().to_string(),
            "method": req.method().as_str(),
            "headers": headers,
            "body": String::from_utf8_lossy(req.raw_body()).to_string(),
        });

        let app = self.clone();
        tokio::spawn(async move {
            if let Ok(query) = app.query(IP_ACCESS_RULE_MODEL) {
                let _ = query.skip_before_commit().create(data).await;
            }
        });
    }

    fn security(&self) -> &Security {
        self.security_state()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn token_bucket_limits_and_refills() {
        let mut bucket = TokenBucket::new(60.0, 2.0); // 1/sec, burst 2
        assert!(bucket.allow());
        assert!(bucket.allow());
        assert!(!bucket.allow(), "burst exhausted");

        // Refill one token after a simulated second.
        bucket.last_seen = Instant::now() - Duration::from_secs(1);
        assert!(bucket.allow());
        assert!(!bucket.allow());
    }

    #[test]
    fn error_guard_blocks_after_the_threshold() {
        let guard = ErrorGuard {
            counters: Mutex::new(HashMap::new()),
            blocked: RwLock::new(HashMap::new()),
            status_codes: [404u16].into_iter().collect(),
            requests_per_second: 3,
            block_duration: None,
        };
        assert!(guard.tracks(404));
        assert!(!guard.tracks(200));

        // Three are allowed; the fourth in the same second trips the block.
        assert!(!guard.record_error("ip"));
        assert!(!guard.record_error("ip"));
        assert!(!guard.record_error("ip"));
        assert!(guard.record_error("ip"), "crossed the threshold");
        assert!(!guard.record_error("ip"), "already blocked");
        assert!(guard.is_blocked("ip"));
        assert!(!guard.is_blocked("other"));
    }

    #[test]
    fn client_key_trusts_proxy_only_when_configured() {
        let mut headers = HeaderMap::new();
        headers.insert("x-forwarded-for", "1.1.1.1, 2.2.2.2".parse().unwrap());
        let peer = Some("9.9.9.9:5000".parse().unwrap());

        assert_eq!(client_key(&headers, peer, true), "1.1.1.1");
        assert_eq!(client_key(&headers, peer, false), "9.9.9.9");
    }
}
