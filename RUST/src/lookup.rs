//! Cached per-request lookups: the tenant for a host, the TenantCatch record
//! for a domain, and users on an authorization server (port of
//! `yekonga/lookup_cache.go`).
//!
//! Writes through [`crate::ModelQuery`] clear the matching cache right away, so
//! the TTL only bounds how long changes made elsewhere (another instance, or
//! the database directly) take to show.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde_json::{json, Value};

use crate::app::Yekonga;
use crate::db::values::object_id_from_str;
use crate::db::DataMap;

pub(crate) const TENANT_MODEL: &str = "Tenant";
pub(crate) const TENANT_CONFIG_MODEL: &str = "TenantConfig";
pub(crate) const TENANT_CATCH_MODEL: &str = "TenantCatch";
pub(crate) const USER_MODEL: &str = "User";

const DEFAULT_TENANT_TTL: Duration = Duration::from_secs(30);
/// Also how long a role or permission change made elsewhere takes to apply.
const DEFAULT_USER_TTL: Duration = Duration::from_secs(10);
/// Hosts come from client headers, so a client can invent many.
const TENANT_MAX_ENTRIES: usize = 10_000;
const USER_MAX_ENTRIES: usize = 50_000;

/// A small TTL cache. A disabled cache (TTL not positive) never stores.
pub(crate) struct LookupCache<V> {
    entries: Mutex<HashMap<String, (V, Instant)>>,
    ttl: Option<Duration>,
    max_entries: usize,
}

impl<V: Clone> LookupCache<V> {
    fn new(ttl: Option<Duration>, max_entries: usize) -> Self {
        Self {
            entries: Mutex::default(),
            ttl,
            max_entries,
        }
    }

    pub fn get(&self, key: &str) -> Option<V> {
        self.ttl?;
        let entries = self.entries.lock().unwrap_or_else(|e| e.into_inner());
        let (value, expires) = entries.get(key)?;
        (Instant::now() < *expires).then(|| value.clone())
    }

    pub fn set(&self, key: &str, value: V) {
        let Some(ttl) = self.ttl else {
            return;
        };
        let mut entries = self.entries.lock().unwrap_or_else(|e| e.into_inner());

        if entries.len() >= self.max_entries {
            let now = Instant::now();
            entries.retain(|_, (_, expires)| now < *expires);
            if entries.len() >= self.max_entries {
                entries.clear();
            }
        }

        entries.insert(key.to_string(), (value, Instant::now() + ttl));
    }

    pub fn clear(&self) {
        self.entries
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clear();
    }
}

/// `cache.*Seconds`: 0 means the default, negative disables.
fn ttl(seconds: i64, default: Duration) -> Option<Duration> {
    match seconds {
        0 => Some(default),
        s if s < 0 => None,
        s => Some(Duration::from_secs(s as u64)),
    }
}

/// A tenant and its TenantConfig record; both `None` when no tenant matches.
#[derive(Clone, Default)]
pub(crate) struct TenantRecords {
    pub tenant: Option<DataMap>,
    pub config: Option<DataMap>,
}

pub(crate) struct LookupCaches {
    pub tenant: LookupCache<TenantRecords>,
    pub tenant_catch: LookupCache<Option<DataMap>>,
    pub user: LookupCache<DataMap>,
}

impl LookupCaches {
    pub fn new(config: &crate::YekongaConfig) -> Self {
        let tenant_ttl = ttl(config.cache.tenant_seconds, DEFAULT_TENANT_TTL);
        Self {
            tenant: LookupCache::new(tenant_ttl, TENANT_MAX_ENTRIES),
            tenant_catch: LookupCache::new(tenant_ttl, TENANT_MAX_ENTRIES),
            user: LookupCache::new(
                ttl(config.cache.user_seconds, DEFAULT_USER_TTL),
                USER_MAX_ENTRIES,
            ),
        }
    }
}

impl Yekonga {
    /// The Tenant whose domain, subdomain, custom domain or custom subdomain
    /// is `host`, with its TenantConfig record.
    pub(crate) async fn tenant_by_host(&self, host: &str) -> TenantRecords {
        if let Some(cached) = self.caches().tenant.get(host) {
            return cached;
        }

        let mut records = TenantRecords::default();
        if let Ok(query) = self.query(TENANT_MODEL) {
            let tenant = query
                .skip_tenant()
                .skip_before_commit()
                .where_many(json!({"OR": [
                    {"domain": host}, {"subdomain": host}, {"customDomain": host}, {"customSubdomain": host}
                ]}))
                .find_one()
                .await
                .unwrap_or_else(|err| {
                    tracing::error!(%err, host, "tenant lookup failed");
                    None
                });

            if let (Some(tenant), true) = (&tenant, self.models().contains_key(TENANT_CONFIG_MODEL))
            {
                let tenant_id = tenant.get("id").cloned().unwrap_or(Value::Null);
                records.config = match self.query(TENANT_CONFIG_MODEL) {
                    Ok(q) => q
                        .skip_tenant()
                        .where_("tenantId", tenant_id)
                        .find_one()
                        .await
                        .ok()
                        .flatten(),
                    Err(_) => None,
                };
            }
            records.tenant = tenant;
        }

        self.caches().tenant.set(host, records.clone());
        records
    }

    /// The TenantCatch record for `domain`. Misses are cached too.
    pub(crate) async fn tenant_catch_by_domain(&self, domain: &str) -> Option<DataMap> {
        if let Some(cached) = self.caches().tenant_catch.get(domain) {
            return cached;
        }

        let record = match self.query(TENANT_CATCH_MODEL) {
            Ok(q) => q
                .where_("domain", domain)
                .find_one()
                .await
                .unwrap_or_else(|err| {
                    tracing::error!(%err, domain, "tenant catch lookup failed");
                    None
                }),
            Err(_) => None,
        };

        self.caches().tenant_catch.set(domain, record.clone());
        record
    }

    /// The User record for `id`. Only found users are cached.
    pub(crate) async fn user_by_id(&self, id: &str) -> Option<DataMap> {
        if let Some(user) = self.caches().user.get(id) {
            return Some(user);
        }

        let user = self
            .query(USER_MODEL)
            .ok()?
            .skip_before_commit()
            .where_("_id", object_id_from_str(id))
            .find_one()
            .await
            .unwrap_or_else(|err| {
                tracing::error!(%err, "user lookup failed");
                None
            })?;

        self.caches().user.set(id, user.clone());
        Some(user)
    }

    /// Drops cached lookups built from `model` after a write to it.
    pub(crate) fn invalidate_caches(&self, model: &str) {
        match model {
            TENANT_MODEL | TENANT_CONFIG_MODEL => self.caches().tenant.clear(),
            TENANT_CATCH_MODEL => self.caches().tenant_catch.clear(),
            USER_MODEL => self.caches().user.clear(),
            _ => {}
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cache_ttl_and_limits() {
        let cache = LookupCache::new(Some(Duration::from_secs(60)), 2);
        cache.set("a", 1);
        cache.set("b", 2);
        assert_eq!(cache.get("a"), Some(1));
        cache.set("c", 3);
        assert_eq!(cache.get("a"), None, "full of live entries: cleared");
        assert_eq!(cache.get("c"), Some(3));

        let disabled = LookupCache::new(None, 2);
        disabled.set("a", 1);
        assert_eq!(disabled.get("a"), None);

        assert_eq!(ttl(0, DEFAULT_USER_TTL), Some(DEFAULT_USER_TTL));
        assert_eq!(ttl(-1, DEFAULT_USER_TTL), None);
        assert_eq!(ttl(5, DEFAULT_USER_TTL), Some(Duration::from_secs(5)));
    }
}
