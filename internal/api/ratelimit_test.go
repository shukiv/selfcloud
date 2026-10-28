package api

import (
	"errors"
	"testing"
	"time"

	"selfcloud/internal/apierr"
)

func TestLimiterBurstThenRetryAfter(t *testing.T) {
	l := newLimiter(3)
	for i := 0; i < 3; i++ {
		if err := l.allow("a"); err != nil {
			t.Fatalf("attempt %d refused: %v", i+1, err)
		}
	}
	err := l.allow("a")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != apierr.RateLimited {
		t.Fatalf("fourth attempt = %v, want rate_limited", err)
	}
	// Three a minute means about 20 seconds per attempt; the wait must be in that range.
	if e.RetryAfter <= 0 || e.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v", e.RetryAfter)
	}
	// A refused attempt takes nothing, so the bucket is still empty, not deeper in debt.
	if err := l.allow("a"); err == nil {
		t.Error("refused attempt refilled the bucket")
	}
	// Other keys have their own buckets.
	if err := l.allow("b"); err != nil {
		t.Errorf("key b refused: %v", err)
	}
}

func TestLimiterSweepsIdleKeys(t *testing.T) {
	l := newLimiter(5)
	_ = l.allow("old")
	_ = l.allow("fresh")
	l.buckets["old"].seen = time.Now().Add(-2 * limiterIdle)
	l.lastSweep = time.Time{}
	_ = l.allow("fresh")
	if _, ok := l.buckets["old"]; ok {
		t.Error("idle bucket was kept")
	}
	if _, ok := l.buckets["fresh"]; !ok {
		t.Error("active bucket was dropped")
	}
}
