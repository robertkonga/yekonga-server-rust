package yekonga

import (
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
)

const (
	tenantModelName      = "Tenant"
	tenantCatchModelName = "TenantCatch"
	userModelName        = "User"

	defaultTenantCacheTTL = 30 * time.Second
	// Shorter than the tenant TTL: it's also how long a role or permission
	// change made outside this process takes to apply.
	defaultUserCacheTTL = 10 * time.Second

	// Tenant hosts come from the client's Origin/Referer headers, so a client
	// can make up as many as it likes; user IDs come from signed tokens.
	tenantCacheMaxEntries = 10000
	userCacheMaxEntries   = 50000
)

// lookupCache is a small TTL cache for per-request database lookups. A nil
// *lookupCache is a disabled cache: every get misses and set/clear do nothing.
//
// Cached values are shared between requests: treat them as read-only.
type lookupCache[V any] struct {
	mut        sync.RWMutex
	entries    map[string]lookupCacheEntry[V]
	ttl        time.Duration
	maxEntries int
}

type lookupCacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

// newLookupCache returns nil (disabled) when ttl isn't positive.
func newLookupCache[V any](ttl time.Duration, maxEntries int) *lookupCache[V] {
	if ttl <= 0 {
		return nil
	}

	return &lookupCache[V]{
		entries:    make(map[string]lookupCacheEntry[V]),
		ttl:        ttl,
		maxEntries: maxEntries,
	}
}

// cacheTTL reads a config.Cache setting: 0 means the default, a negative
// value disables the cache.
func cacheTTL(seconds int, defaultTTL time.Duration) time.Duration {
	if seconds == 0 {
		return defaultTTL
	}

	if seconds < 0 {
		return 0
	}

	return time.Duration(seconds) * time.Second
}

func (c *lookupCache[V]) get(key string) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}

	c.mut.RLock()
	entry, ok := c.entries[key]
	c.mut.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return zero, false
	}

	return entry.value, true
}

func (c *lookupCache[V]) set(key string, value V) {
	if c == nil {
		return
	}

	c.mut.Lock()
	defer c.mut.Unlock()

	if len(c.entries) >= c.maxEntries {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}

		if len(c.entries) >= c.maxEntries {
			c.entries = make(map[string]lookupCacheEntry[V])
		}
	}

	c.entries[key] = lookupCacheEntry[V]{value: value, expiresAt: time.Now().Add(c.ttl)}
}

func (c *lookupCache[V]) clear() {
	if c == nil {
		return
	}

	c.mut.Lock()
	c.entries = make(map[string]lookupCacheEntry[V])
	c.mut.Unlock()
}

// tenantRecords is what a host resolves to. Both are nil when no tenant
// matches the host, which is cached too so unknown hosts don't reach the
// database on every request.
type tenantRecords struct {
	tenant       *datatype.DataMap
	tenantConfig *datatype.DataMap
}

func (y *YekongaData) initLookupCaches() {
	y.tenantCache = newLookupCache[tenantRecords](cacheTTL(y.Config.Cache.TenantSeconds, defaultTenantCacheTTL), tenantCacheMaxEntries)
	y.tenantCatchCache = newLookupCache[*datatype.DataMap](cacheTTL(y.Config.Cache.TenantSeconds, defaultTenantCacheTTL), tenantCacheMaxEntries)
	y.userCache = newLookupCache[*datatype.DataMap](cacheTTL(y.Config.Cache.UserSeconds, defaultUserCacheTTL), userCacheMaxEntries)
}

// tenantByHost returns the Tenant whose domain, subdomain, custom domain or
// custom subdomain is host, with its TenantConfig record. Either is nil when
// there's no match.
func (y *YekongaData) tenantByHost(host string) (tenant *datatype.DataMap, tenantConfig *datatype.DataMap) {
	if cached, ok := y.tenantCache.get(host); ok {
		return cached.tenant, cached.tenantConfig
	}

	tenant = y.ModelQuery(tenantModelName).SkipTenant().SkipBeforeCommit().FindOne(datatype.DataMap{
		"OR": []datatype.DataMap{
			{"domain": host},
			{"subdomain": host},
			{"customDomain": host},
			{"customSubdomain": host},
		},
	})

	if helper.IsEmpty(tenant) {
		tenant = nil
	} else if _, ok := y.models[tenantConfigModelName]; ok {
		tenantConfig = y.ModelQuery(tenantConfigModelName).SkipTenant().SkipBeforeCommit().FindOne(datatype.DataMap{
			"tenantId": helper.GetValueOf(tenant, "id"),
		})

		if helper.IsEmpty(tenantConfig) {
			tenantConfig = nil
		}
	}

	y.tenantCache.set(host, tenantRecords{tenant: tenant, tenantConfig: tenantConfig})

	return tenant, tenantConfig
}

// tenantCatchByDomain returns the TenantCatch record for domain, or nil.
// Only the database lookup is cached, never the FetchTenantByDomain cloud
// function: that's user code and may have side effects. A miss is cached
// too; if the cloud function then records the domain, that write clears the
// cache.
func (y *YekongaData) tenantCatchByDomain(domain interface{}) *datatype.DataMap {
	key, cacheable := domain.(string)

	if cacheable {
		if cached, ok := y.tenantCatchCache.get(key); ok {
			return cached
		}
	}

	record := y.ModelQuery(tenantCatchModelName).SkipBeforeCommit().FindOne(datatype.DataMap{
		"domain": domain,
	})

	if helper.IsEmpty(record) {
		record = nil
	}

	if cacheable {
		y.tenantCatchCache.set(key, record)
	}

	return record
}

// userById returns the User record for id, or nil. Each call gets its own
// copy of the cached record, since it's stored on the request context where
// handlers can reach it.
func (y *YekongaData) userById(id string) *datatype.DataMap {
	user, ok := y.userCache.get(id)

	if !ok {
		user = y.ModelQuery(userModelName).SkipBeforeCommit().FindOne(datatype.DataMap{
			"_id": helper.ObjectID(id),
		})

		if helper.IsEmpty(user) {
			return nil
		}

		// Only found users are cached: a token for a missing user is rare, and
		// caching the miss would hide a user created right after.
		y.userCache.set(id, user)
	}

	userCopy := make(datatype.DataMap, len(*user))
	for k, v := range *user {
		userCopy[k] = v
	}

	return &userCopy
}

// invalidateCaches drops cached lookups after a write through DataModelQuery
// to a model they're built from. Those writes are infrequent next to the
// reads (User is written on login), so clearing a whole cache is simpler than
// working out which keys a write affects.
func (y *YekongaData) invalidateCaches(modelName string) {
	switch modelName {
	case tenantModelName, tenantConfigModelName:
		y.tenantCache.clear()
	case tenantCatchModelName:
		y.tenantCatchCache.clear()
	case userModelName:
		y.userCache.clear()
	}
}
