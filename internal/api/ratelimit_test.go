// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
)

func TestLimiterBurstThenRetryAfter(t *testing.T) {
	t.Parallel()
	l := newLimiter(3)
	for i := 0; i < 3; i++ {
		if err := l.allow(frozenLimitInstant, "a"); err != nil {
			t.Fatalf("attempt %d refused: %v", i+1, err)
		}
	}
	err := l.allow(frozenLimitInstant, "a")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != apierr.RateLimited {
		t.Fatalf("fourth attempt = %v, want rate_limited", err)
	}
	// Three a minute means about 20 seconds per attempt; the wait must be in that range.
	if e.RetryAfter <= 0 || e.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v", e.RetryAfter)
	}
	// A refused attempt takes nothing, so the bucket is still empty, not deeper in debt.
	if err := l.allow(frozenLimitInstant, "a"); err == nil {
		t.Error("refused attempt refilled the bucket")
	}
	// Other keys have their own buckets.
	if err := l.allow(frozenLimitInstant, "b"); err != nil {
		t.Errorf("key b refused: %v", err)
	}
}

func TestLimiterSweepsIdleKeys(t *testing.T) {
	t.Parallel()
	l := newLimiter(5)
	_ = l.allow(frozenLimitInstant, "old")
	_ = l.allow(frozenLimitInstant, "fresh")
	// Two minutes on, "old" has been idle past the limit and has refilled to full; "fresh" was used
	// just now.
	later := frozenLimitInstant.Add(2 * time.Minute)
	l.buckets["old"].seen = later.Add(-2 * limiterIdle)
	l.buckets["fresh"].seen = later
	l.sweep(later)
	if _, ok := l.buckets["old"]; ok {
		t.Error("idle bucket was kept")
	}
	if _, ok := l.buckets["fresh"]; !ok {
		t.Error("active bucket was dropped")
	}
}

// TestHourlyBucketSurvivesSweepUntilFull checks the sweep: an idle bucket of an hourly limiter has
// not refilled, so it stays, and its key is still refused. A per-minute bucket is full again within
// a minute, so it is removed.
func TestHourlyBucketSurvivesSweepUntilFull(t *testing.T) {
	t.Parallel()
	hourly := newLimiterRate(3, time.Hour)
	t0 := frozenLimitInstant
	for range 3 {
		if _, err := hourly.reserve("a@example.com", t0); err != nil {
			t.Fatal(err)
		}
	}
	// Eleven minutes later the bucket is idle, but holds only a sixth of its budget.
	later := t0.Add(11 * time.Minute)
	hourly.sweep(later)
	if _, ok := hourly.buckets["a@example.com"]; !ok {
		t.Fatal("an hourly bucket was swept before it refilled")
	}
	if _, err := hourly.reserve("a@example.com", later); err == nil {
		t.Fatal("key got a fresh budget after 11 minutes of an hourly limit")
	}

	perMinute := newLimiter(2)
	if _, err := perMinute.reserve("b", t0); err != nil {
		t.Fatal(err)
	}
	perMinute.sweep(t0.Add(11 * time.Minute))
	if _, ok := perMinute.buckets["b"]; ok {
		t.Error("a full per-minute bucket was not swept")
	}
}

// TestRateLimitTakesTimeFromTheServerClock: a limiter reads the server's clock, not the wall clock,
// so a test that freezes the clock gets the same answers on every run.
func TestRateLimitTakesTimeFromTheServerClock(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	now := frozenLimitInstant
	s.limitClock = func() time.Time { return now }
	s.limits["probe"] = newLimiter(2)
	for i := 0; i < 2; i++ {
		if err := s.rateLimit("probe", "k"); err != nil {
			t.Fatalf("attempt %d refused: %v", i+1, err)
		}
	}
	if err := s.rateLimit("probe", "k"); err == nil {
		t.Fatal("third attempt allowed on a frozen clock")
	}
	now = now.Add(time.Minute)
	if err := s.rateLimit("probe", "k"); err != nil {
		t.Errorf("one minute later the bucket has a token: %v", err)
	}
}
