package yekonga

import (
	"sync"
	"time"

	"github.com/robertkonga/yekonga-server-go/helper/console"
)

// onceLogger logs each distinct message key once. It stops tracking new
// keys past a cap, since keys can come from client input (e.g. accessRole).
type onceLogger struct {
	mut  sync.Mutex
	seen map[string]bool
	max  int
}

func newOnceLogger(max int) *onceLogger {
	return &onceLogger{seen: make(map[string]bool), max: max}
}

// first reports whether key hasn't been logged yet, and records it.
func (l *onceLogger) first(key string) bool {
	l.mut.Lock()
	defer l.mut.Unlock()

	if l.seen[key] || len(l.seen) >= l.max {
		return false
	}

	l.seen[key] = true
	return true
}

// missingTriggerLog: a model with triggers for some roles gets queried with
// other roles all the time; one warning per model/action/role is enough.
var missingTriggerLog = newOnceLogger(1000)

// rateLimitedLogger allows up to perSecond messages a second and then
// reports how many it skipped.
type rateLimitedLogger struct {
	mut       sync.Mutex
	perSecond int
	window    time.Time
	count     int
	skipped   int
}

func (l *rateLimitedLogger) allow() (ok bool, skippedBefore int) {
	l.mut.Lock()
	defer l.mut.Unlock()

	now := time.Now().Truncate(time.Second)
	if !now.Equal(l.window) {
		skippedBefore = l.skipped
		l.window, l.count, l.skipped = now, 0, 0
	}

	if l.count >= l.perSecond {
		l.skipped++
		return false, skippedBefore
	}

	l.count++
	return true, skippedBefore
}

// clientErrorLog limits logging of 4xx responses: a scan or brute-force
// attempt produces thousands a second, and writing each one out is slow.
var clientErrorLog = &rateLimitedLogger{perSecond: 10}

// logAbort logs an aborted request: server errors always, client errors
// within clientErrorLog's limit.
func logAbort(code int, message string) {
	if code >= 500 {
		console.Error("Abort:", code, message)
		return
	}

	ok, skipped := clientErrorLog.allow()
	if skipped > 0 {
		console.Error("Abort:", skipped, "more client error responses not logged")
	}
	if ok {
		console.Error("Abort:", code, message)
	}
}
