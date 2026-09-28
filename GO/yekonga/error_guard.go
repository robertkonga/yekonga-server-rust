package yekonga

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
)

// errorGuardRefreshInterval controls how often the guard re-syncs its
// blocked state from the IpAccessRule collection, so a block tripped on
// another replica, a manually added row, or an unblock (deleting a row)
// becomes effective here too — not just on the instance that first saw the
// abuse. Whitelist rows are handled separately by ipWhitelistStore, shared
// with the rate limiter.
const errorGuardRefreshInterval = 30 * time.Second

// defaultErrorGuardStatusCodes is used when config.Security.ErrorGuard.StatusCodes
// is empty: the classic signature of scanning/enumeration/brute-force traffic
// (missing/invalid credentials, forbidden, not found) rather than a real user.
var defaultErrorGuardStatusCodes = []int{
	http.StatusBadRequest,
	http.StatusUnauthorized,
	http.StatusForbidden,
	http.StatusNotFound,
}

// permanentBlockUntil is a sentinel "unblock time" far enough in the future
// to be effectively permanent, so blocked entries can share the same
// map[string]time.Time as timed ones instead of needing a parallel set.
var permanentBlockUntil = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// errorCounter is a fixed one-second window counter: it resets whenever a
// hit lands in a different second than the one it's currently tracking.
type errorCounter struct {
	second int64
	count  int
}

// errorGuard blocks clients that flood the server with error responses
// (401/403/404/etc.) within the same second — the signature of automated
// scanning, credential stuffing, or endpoint enumeration rather than a real
// user, and a cheap way for an attacker to burn CPU/IO on auth checks and
// error rendering. A trip is treated as an attack: the source is blocked
// permanently unless config.Security.ErrorGuard.BlockHours says otherwise.
type errorGuard struct {
	mut               sync.Mutex
	counters          map[string]*errorCounter // client key -> current-second error counter
	blocked           map[string]time.Time     // client key -> unblock time (permanentBlockUntil = forever)
	statusCodes       map[int]bool             // status codes this guard counts as abuse
	requestsPerSecond int
	permanent         bool
	blockDuration     time.Duration // only used when !permanent
}

func newErrorGuard(statusCodes []int, requestsPerSecond int, permanent bool, blockDuration time.Duration) *errorGuard {
	codes := make(map[int]bool, len(statusCodes))
	for _, code := range statusCodes {
		codes[code] = true
	}

	guard := &errorGuard{
		counters:          make(map[string]*errorCounter),
		blocked:           make(map[string]time.Time),
		statusCodes:       codes,
		requestsPerSecond: requestsPerSecond,
		permanent:         permanent,
		blockDuration:     blockDuration,
	}

	go guard.evictLoop()

	return guard
}

// tracks reports whether statusCode counts toward the abuse threshold.
func (g *errorGuard) tracks(statusCode int) bool {
	return g.statusCodes[statusCode]
}

// isBlocked reports whether key is currently serving out a block.
func (g *errorGuard) isBlocked(key string) bool {
	g.mut.Lock()
	defer g.mut.Unlock()

	until, ok := g.blocked[key]
	if !ok {
		return false
	}

	if time.Now().After(until) {
		delete(g.blocked, key)
		return false
	}

	return true
}

// syncFromRows rebuilds the blocked map from the current set of
// IpAccessRule "blacklist" rows, replacing whatever this process had
// accumulated locally since the last sync. A row with no expiresAt blocks
// permanently; one with a past expiresAt is dropped as already expired.
// Whitelist rows are ignored here — ipWhitelistStore handles those.
func (g *errorGuard) syncFromRows(rows []datatype.DataMap) {
	blocked := make(map[string]time.Time, len(rows))
	now := time.Now()

	for _, row := range rows {
		if helper.ToString(row["type"]) != "blacklist" {
			continue
		}

		ip := helper.ToString(row["ipAddress"])
		if ip == "" {
			continue
		}

		until := permanentBlockUntil

		if expiresAt := helper.StringToDatetime(row["expiresAt"]); expiresAt != nil {
			if expiresAt.Before(now) {
				continue // already expired
			}
			until = *expiresAt
		}

		blocked[ip] = until
	}

	g.mut.Lock()
	g.blocked = blocked
	g.mut.Unlock()
}

// recordError registers one tracked error response for key. If key has now
// sent more than requestsPerSecond of them within the same second, it is
// blocked (permanently, unless configured with a fixed blockDuration). It
// reports true only the moment it causes that transition into "blocked", so
// the caller persists the block record exactly once.
func (g *errorGuard) recordError(key string) bool {
	g.mut.Lock()
	defer g.mut.Unlock()

	now := time.Now()
	second := now.Unix()

	counter, ok := g.counters[key]
	if !ok || counter.second != second {
		counter = &errorCounter{second: second}
		g.counters[key] = counter
	}

	counter.count++

	if counter.count <= g.requestsPerSecond {
		return false
	}

	delete(g.counters, key)

	if _, alreadyBlocked := g.blocked[key]; alreadyBlocked {
		return false
	}

	until := permanentBlockUntil
	if !g.permanent {
		until = now.Add(g.blockDuration)
	}
	g.blocked[key] = until

	return true
}

// evictLoop periodically drops stale entries so memory doesn't grow
// unbounded under a sustained flood from many distinct/spoofed source IPs.
// Permanently blocked entries (permanentBlockUntil) are never evicted here.
func (g *errorGuard) evictLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		g.mut.Lock()
		for key, until := range g.blocked {
			if until != permanentBlockUntil && now.After(until) {
				delete(g.blocked, key)
			}
		}
		for key, counter := range g.counters {
			if now.Unix()-counter.second > 5 {
				delete(g.counters, key)
			}
		}
		g.mut.Unlock()
	}
}

// ensureErrorGuard lazily builds the guard from config, once.
func (y *YekongaData) ensureErrorGuard() {
	y.errorGuardOnce.Do(func() {
		cfg := y.Config.Security.ErrorGuard

		statusCodes := defaultErrorGuardStatusCodes
		if len(cfg.StatusCodes) > 0 {
			statusCodes = cfg.StatusCodes
		}

		requestsPerSecond := cfg.RequestsPerSecond
		if requestsPerSecond <= 0 {
			requestsPerSecond = 20
		}

		// A trip is treated as an attack: block permanently by default.
		// Set blockHours to fall back to a timed block instead.
		permanent := cfg.BlockHours <= 0
		var blockDuration time.Duration
		if !permanent {
			blockDuration = time.Duration(cfg.BlockHours) * time.Hour
		}

		y.errorGuard = newErrorGuard(statusCodes, requestsPerSecond, permanent, blockDuration)

		// Load existing blacklist rows immediately, so a restarted or freshly
		// started replica honors blocks an admin (or another replica)
		// already put in the database, without waiting for the first
		// periodic refresh below.
		y.refreshErrorGuardFromDB()

		go func() {
			ticker := time.NewTicker(errorGuardRefreshInterval)
			defer ticker.Stop()

			for range ticker.C {
				y.refreshErrorGuardFromDB()
			}
		}()
	})
}

// refreshErrorGuardFromDB reloads the guard's blocked state from the
// IpAccessRule collection. Safe to call before the model is available (e.g.
// very early in startup) — it simply no-ops until it is.
func (y *YekongaData) refreshErrorGuardFromDB() {
	model := y.ModelQuery("IpAccessRule")
	if model == nil {
		return
	}

	rows := model.SkipBeforeCommit().Find(nil)
	if rows == nil {
		return
	}

	y.errorGuard.syncFromRows(*rows)
}

// checkErrorGuardBlock reports whether the request may proceed. When the
// client is currently blocked it writes a 403 itself and returns false, so
// the caller should stop handling the request immediately. It is a no-op
// (always allows) when config.Security.ErrorGuard.Enabled is false.
//
// This runs before anything else in ServeHTTP, including static file
// lookups, so a blocked source gets nothing back for the duration of the block.
func (y *YekongaData) checkErrorGuardBlock(w http.ResponseWriter, r *http.Request) bool {
	if !y.Config.Security.ErrorGuard.Enabled {
		return true
	}

	if y.isIPWhitelisted(r) {
		return true
	}

	y.ensureErrorGuard()

	key := clientIP(r, y.Config.Security.TrustProxyHeaders)

	if !y.errorGuard.isBlocked(key) {
		return true
	}

	http.Error(w, "forbidden", http.StatusForbidden)

	return false
}

// recordErrorResponse registers one response of the given status code
// against req's client, if that status is one the guard tracks. The first
// time this trips the guard's block for a client, it also persists the
// block — with the reason, URL, headers and body of the request that
// tripped it — to the IpAccessRule collection, so the block is visible to
// admins and auditable after the fact. No-op when
// config.Security.ErrorGuard.Enabled is false.
func (y *YekongaData) recordErrorResponse(req *Request, statusCode int) {
	if !y.Config.Security.ErrorGuard.Enabled || req == nil || req.HttpRequest == nil {
		return
	}

	if y.isIPWhitelisted(req.HttpRequest) {
		return
	}

	y.ensureErrorGuard()

	if !y.errorGuard.tracks(statusCode) {
		return
	}

	key := clientIP(req.HttpRequest, y.Config.Security.TrustProxyHeaders)

	if y.errorGuard.recordError(key) {
		y.persistIpBlock(req, key, statusCode)
	}
}

// persistIpBlock writes a permanent (or config-timed) blacklist entry for ip
// to the IpAccessRule collection, capturing the request that tripped the
// guard. It runs in the background so a burst of trips can never add
// latency to the responses already in flight.
func (y *YekongaData) persistIpBlock(req *Request, ip string, statusCode int) {
	cfg := y.Config.Security.ErrorGuard

	go func() {
		model := y.ModelQuery("IpAccessRule")
		if model == nil {
			return
		}

		data := datatype.DataMap{
			"ipAddress": ip,
			"type":      "blacklist",
			"source":    "errorGuard",
			"reason": fmt.Sprintf(
				"exceeded %d error responses/second (tripped by %d %s)",
				cfg.RequestsPerSecond, statusCode, http.StatusText(statusCode),
			),
			"url":     req.HttpRequest.RequestURI,
			"method":  req.HttpRequest.Method,
			"headers": req.HttpRequest.Header,
			"body":    req.RawBody,
		}

		model.SkipBeforeCommit().Create(data)
	}()
}
