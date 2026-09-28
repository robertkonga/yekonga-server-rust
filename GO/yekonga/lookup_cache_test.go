package yekonga

import (
	"fmt"
	"sync"
	"testing"
	"time"

	mainConfig "github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
)

func TestLookupCache(t *testing.T) {
	c := newLookupCache[string](time.Minute, 10)

	if _, ok := c.get("a"); ok {
		t.Fatal("empty cache returned a hit")
	}

	c.set("a", "one")
	if v, ok := c.get("a"); !ok || v != "one" {
		t.Fatalf("get(a) = (%q, %v), want (\"one\", true)", v, ok)
	}

	c.clear()
	if _, ok := c.get("a"); ok {
		t.Fatal("clear left an entry behind")
	}
}

func TestLookupCacheExpires(t *testing.T) {
	c := newLookupCache[string](time.Millisecond, 10)
	c.set("a", "one")
	time.Sleep(5 * time.Millisecond)

	if _, ok := c.get("a"); ok {
		t.Fatal("expired entry returned a hit")
	}
}

func TestLookupCacheIsBounded(t *testing.T) {
	c := newLookupCache[int](time.Minute, 100)

	for i := 0; i < 250; i++ {
		c.set(fmt.Sprintf("host-%d", i), i)
	}

	if n := len(c.entries); n > 100 {
		t.Fatalf("cache holds %d entries, cap is 100", n)
	}
}

func TestDisabledLookupCache(t *testing.T) {
	c := newLookupCache[string](0, 10)
	if c != nil {
		t.Fatal("a zero TTL should give a disabled (nil) cache")
	}

	// A disabled cache must be safe to use.
	c.set("a", "one")
	c.clear()
	if _, ok := c.get("a"); ok {
		t.Fatal("disabled cache returned a hit")
	}
}

func TestCacheTTL(t *testing.T) {
	tests := []struct {
		seconds int
		want    time.Duration
	}{
		{0, 30 * time.Second},
		{-1, 0},
		{5, 5 * time.Second},
	}

	for _, tt := range tests {
		if got := cacheTTL(tt.seconds, 30*time.Second); got != tt.want {
			t.Errorf("cacheTTL(%d) = %v, want %v", tt.seconds, got, tt.want)
		}
	}
}

func newTestCacheServer(cfg mainConfig.YekongaConfig) *YekongaData {
	y := &YekongaData{Config: &cfg}
	y.initLookupCaches()

	return y
}

func TestInitLookupCachesHonoursConfig(t *testing.T) {
	var cfg mainConfig.YekongaConfig
	cfg.Cache.UserSeconds = -1

	y := newTestCacheServer(cfg)
	if y.userCache != nil {
		t.Error("userSeconds < 0 should disable the user cache")
	}

	if y.tenantCache == nil || y.tenantCache.ttl != defaultTenantCacheTTL {
		t.Error("tenantSeconds = 0 should use the default tenant TTL")
	}
}

func TestInvalidateCaches(t *testing.T) {
	y := newTestCacheServer(mainConfig.YekongaConfig{})

	fill := func() {
		y.tenantCache.set("host", tenantRecords{})
		y.tenantCatchCache.set("host", nil)
		y.userCache.set("id", &datatype.DataMap{})
	}

	cached := func() (tenant, tenantCatch, user bool) {
		_, tenant = y.tenantCache.get("host")
		_, tenantCatch = y.tenantCatchCache.get("host")
		_, user = y.userCache.get("id")
		return
	}

	tests := []struct {
		model                     string
		tenant, tenantCatch, user bool // still cached after the write
	}{
		{"Order", true, true, true},
		{tenantModelName, false, true, true},
		{tenantConfigModelName, false, true, true},
		{tenantCatchModelName, true, false, true},
		{userModelName, true, true, false},
	}

	for _, tt := range tests {
		fill()
		y.invalidateCaches(tt.model)

		tenant, tenantCatch, user := cached()
		if tenant != tt.tenant || tenantCatch != tt.tenantCatch || user != tt.user {
			t.Errorf("write to %s: cached (tenant, tenantCatch, user) = (%v, %v, %v), want (%v, %v, %v)",
				tt.model, tenant, tenantCatch, user, tt.tenant, tt.tenantCatch, tt.user)
		}
	}
}

func TestCachedLookupsServeHits(t *testing.T) {
	y := newTestCacheServer(mainConfig.YekongaConfig{})
	tenant := &datatype.DataMap{"_id": "t1"}
	catch := &datatype.DataMap{"tenantId": "t1"}

	y.tenantCache.set("a.example.com", tenantRecords{tenant: tenant})
	y.tenantCatchCache.set("b.example.com", catch)

	// y.models is empty, so a cache miss would panic on the nil model query.
	if got, _ := y.tenantByHost("a.example.com"); got != tenant {
		t.Error("tenantByHost did not serve the cached tenant")
	}

	if got := y.tenantCatchByDomain("b.example.com"); got != catch {
		t.Error("tenantCatchByDomain did not serve the cached record")
	}
}

func TestUserByIdReturnsACopy(t *testing.T) {
	y := newTestCacheServer(mainConfig.YekongaConfig{})
	y.userCache.set("u1", &datatype.DataMap{"username": "alice"})

	first := y.userById("u1")
	(*first)["username"] = "changed by a handler"

	second := y.userById("u1")
	if (*second)["username"] != "alice" {
		t.Fatalf("a change to one request's user info leaked into the cache: got %v", (*second)["username"])
	}
}

func TestLookupCacheConcurrent(t *testing.T) {
	c := newLookupCache[int](time.Minute, 3)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			c.set(fmt.Sprintf("h%d", i%5), i)
		}(i)
		go func(i int) {
			defer wg.Done()
			c.get(fmt.Sprintf("h%d", i%5))
		}(i)
		go func() {
			defer wg.Done()
			c.clear()
		}()
	}
	wg.Wait()
}
