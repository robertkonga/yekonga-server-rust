package yekonga

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// clientRateLimiter is a per-client token bucket: it starts full with `burst`
// tokens and refills at `refillPerSec` tokens/second, capped at `burst`. Each
// allowed request consumes one token.
type clientRateLimiter struct {
	mut          sync.Mutex
	tokens       float64
	burst        float64
	refillPerSec float64
	lastSeen     time.Time
}

func newClientRateLimiter(requestsPerMinute, burst int) *clientRateLimiter {
	return &clientRateLimiter{
		tokens:       float64(burst),
		burst:        float64(burst),
		refillPerSec: float64(requestsPerMinute) / 60,
		lastSeen:     time.Now(),
	}
}

func (l *clientRateLimiter) allow() bool {
	l.mut.Lock()
	defer l.mut.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastSeen).Seconds()
	l.lastSeen = now

	l.tokens += elapsed * l.refillPerSec
	if l.tokens > l.burst {
		l.tokens = l.burst
	}

	if l.tokens < 1 {
		return false
	}

	l.tokens--
	return true
}

func (l *clientRateLimiter) idleSince(cutoff time.Time) bool {
	l.mut.Lock()
	defer l.mut.Unlock()

	return l.lastSeen.Before(cutoff)
}

// rateLimiterStore holds one clientRateLimiter per client key (IP by
// default) and periodically evicts entries that have been idle, so memory
// doesn't grow unbounded under a sustained flood from many distinct sources.
type rateLimiterStore struct {
	mut               sync.Mutex
	limiters          map[string]*clientRateLimiter
	requestsPerMinute int
	burst             int
}

func newRateLimiterStore(requestsPerMinute, burst int) *rateLimiterStore {
	store := &rateLimiterStore{
		limiters:          make(map[string]*clientRateLimiter),
		requestsPerMinute: requestsPerMinute,
		burst:             burst,
	}

	go store.evictLoop()

	return store
}

func (s *rateLimiterStore) allow(key string) bool {
	s.mut.Lock()
	limiter, ok := s.limiters[key]
	if !ok {
		limiter = newClientRateLimiter(s.requestsPerMinute, s.burst)
		s.limiters[key] = limiter
	}
	s.mut.Unlock()

	return limiter.allow()
}

func (s *rateLimiterStore) evictLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)

		s.mut.Lock()
		for key, limiter := range s.limiters {
			if limiter.idleSince(cutoff) {
				delete(s.limiters, key)
			}
		}
		s.mut.Unlock()
	}
}

// clientIP extracts the request's source IP to key the rate limiter on. It
// only trusts X-Forwarded-For/X-Real-Ip when the server is configured to sit
// behind a proxy (trustProxyHeaders); otherwise a client could send a forged
// header to get a fresh bucket on every request and bypass the limiter
// entirely, so it falls back to the raw TCP remote address.
func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
		if xrip := r.Header.Get("X-Real-Ip"); xrip != "" {
			return strings.TrimSpace(xrip)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// checkRateLimit reports whether the request is allowed to proceed. When the
// client has exceeded its budget it writes a 429 response itself and returns
// false, so the caller should stop handling the request immediately. It is a
// no-op (always allows) when config.Security.RateLimit.Enabled is false.
//
// This runs as early as possible in ServeHTTP, before the request body is
// read or parsed, so an abusive client is rejected without paying the cost
// of multipart/JSON parsing on every hit.
func (y *YekongaData) checkRateLimit(w http.ResponseWriter, r *http.Request) bool {
	if !y.Config.Security.RateLimit.Enabled {
		return true
	}

	if y.isIPWhitelisted(r) {
		return true
	}

	y.rateLimiterOnce.Do(func() {
		rpm := y.Config.Security.RateLimit.RequestsPerMinute
		if rpm <= 0 {
			rpm = 300
		}

		burst := y.Config.Security.RateLimit.Burst
		if burst <= 0 {
			burst = rpm
		}

		y.rateLimiter = newRateLimiterStore(rpm, burst)
	})

	key := clientIP(r, y.Config.Security.TrustProxyHeaders)

	if y.rateLimiter.allow(key) {
		return true
	}

	w.Header().Set("Retry-After", "1")
	http.Error(w, "too many requests", http.StatusTooManyRequests)

	return false
}
