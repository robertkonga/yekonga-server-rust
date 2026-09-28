package yekonga

import (
	"net/http"
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
)

// ipWhitelistRefreshInterval controls how often the shared whitelist cache
// re-syncs from the IpAccessRule collection. It is shared by the rate
// limiter and the error guard, and initialized independently of either, so
// a "whitelist" row exempts a client from both once either feature is on —
// and from neither if both are off.
const ipWhitelistRefreshInterval = 30 * time.Second

// ipWhitelistStore is a small read-mostly cache of client keys (IPs) that
// are exempt from this server's abuse guards, loaded from IpAccessRule rows
// with type "whitelist".
type ipWhitelistStore struct {
	mut sync.RWMutex
	ips map[string]bool
}

func newIPWhitelistStore() *ipWhitelistStore {
	return &ipWhitelistStore{ips: make(map[string]bool)}
}

func (s *ipWhitelistStore) isWhitelisted(key string) bool {
	s.mut.RLock()
	defer s.mut.RUnlock()

	return s.ips[key]
}

// syncFromRows rebuilds the whitelist set from the current IpAccessRule
// rows, replacing what was cached before. Non-whitelist rows are ignored
// here — the error guard's own sync handles blacklist rows.
func (s *ipWhitelistStore) syncFromRows(rows []datatype.DataMap) {
	ips := make(map[string]bool)

	for _, row := range rows {
		if helper.ToString(row["type"]) != "whitelist" {
			continue
		}

		if ip := helper.ToString(row["ipAddress"]); ip != "" {
			ips[ip] = true
		}
	}

	s.mut.Lock()
	s.ips = ips
	s.mut.Unlock()
}

// ensureIPWhitelist lazily builds the shared whitelist cache and starts its
// periodic refresh, once. Called from whichever of the rate limiter or the
// error guard is used first.
func (y *YekongaData) ensureIPWhitelist() {
	y.ipWhitelistOnce.Do(func() {
		y.ipWhitelist = newIPWhitelistStore()

		y.refreshIPWhitelistFromDB()

		go func() {
			ticker := time.NewTicker(ipWhitelistRefreshInterval)
			defer ticker.Stop()

			for range ticker.C {
				y.refreshIPWhitelistFromDB()
			}
		}()
	})
}

// refreshIPWhitelistFromDB reloads the whitelist cache from the
// IpAccessRule collection. Safe to call before the model is available — it
// simply no-ops until it is.
func (y *YekongaData) refreshIPWhitelistFromDB() {
	model := y.ModelQuery("IpAccessRule")
	if model == nil {
		return
	}

	rows := model.SkipBeforeCommit().Find(nil)
	if rows == nil {
		return
	}

	y.ipWhitelist.syncFromRows(*rows)
}

// isIPWhitelisted reports whether r's client is exempt from the rate
// limiter and error guard.
func (y *YekongaData) isIPWhitelisted(r *http.Request) bool {
	y.ensureIPWhitelist()

	key := clientIP(r, y.Config.Security.TrustProxyHeaders)

	return y.ipWhitelist.isWhitelisted(key)
}
