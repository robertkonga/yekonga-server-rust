package yekonga

import (
	"fmt"
	"testing"
	"time"
)

func TestOnceLogger(t *testing.T) {
	l := newOnceLogger(2)

	if !l.first("a") || l.first("a") {
		t.Fatal("a key should be logged exactly once")
	}
	if !l.first("b") {
		t.Fatal("a second key should be logged")
	}
	if l.first("c") {
		t.Fatal("keys past the cap should not be tracked or logged")
	}
}

func TestRateLimitedLogger(t *testing.T) {
	l := &rateLimitedLogger{perSecond: 3}

	// Start just after a second boundary so the window doesn't roll mid-test.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 10*time.Millisecond)))

	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.allow(); ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed %d in one second, want 3", allowed)
	}

	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 10*time.Millisecond)))

	ok, skipped := l.allow()
	if !ok || skipped != 7 {
		t.Fatalf("next second: ok=%v skipped=%d, want true and 7", ok, skipped)
	}
}

func BenchmarkLogAbortClientError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		logAbort(404, fmt.Sprint("not found ", i))
	}
}
