package api

import (
	"sync"
	"time"

	"golang.org/x/time/rate"

	"selfcloud/internal/apierr"
)

// limiterIdle is how long a key may go unused before its bucket is forgotten. A bucket that has
// been idle this long has refilled completely, so forgetting it changes nothing.
const limiterIdle = 10 * time.Minute

// limiter gives each key its own token bucket. A bucket holds perMinute tokens and refills at
// perMinute a minute, so a key may make a burst of perMinute attempts and then one a minute per
// slot.
type limiter struct {
	mu        sync.Mutex
	perMinute int
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newLimiter(perMinute int) *limiter {
	return &limiter{perMinute: perMinute, buckets: map[string]*bucket{}}
}

// allow takes one token for key. When none is left it returns rate_limited with the time until
// the next one, and takes nothing.
func (l *limiter) allow(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastSweep) > time.Minute {
		l.sweep(now)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{lim: rate.NewLimiter(rate.Limit(float64(l.perMinute)/60), l.perMinute)}
		l.buckets[key] = b
	}
	b.seen = now
	res := b.lim.ReserveN(now, 1)
	if !res.OK() {
		return apierr.New(apierr.RateLimited, "too many attempts; try again later").Retry(time.Minute)
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return apierr.New(apierr.RateLimited, "too many attempts; try again later").Retry(delay)
	}
	return nil
}

// sweep forgets the buckets that have been idle. The caller holds l.mu.
func (l *limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.seen) > limiterIdle {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}

// rateLimit charges one attempt against the named limiter for key. Names are "endpoint.ip" and
// "endpoint.email". The check runs before any database lookup or password hash, so a flood costs
// little.
func (s *Server) rateLimit(name, key string) error {
	return s.limits[name].allow(key)
}
