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
	limit     rate.Limit // tokens a second
	burst     int        // the most a key may spend at once
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newLimiter(perMinute int) *limiter {
	return newLimiterRate(perMinute, time.Minute)
}

// newLimiterRate allows burst attempts per key and refills burst attempts every window, so
// newLimiterRate(30, time.Hour) allows 30 an hour.
func newLimiterRate(burst int, window time.Duration) *limiter {
	return &limiter{perMinute: burst, limit: rate.Limit(float64(burst) / window.Seconds()), burst: burst,
		buckets: map[string]*bucket{}}
}

// allow takes one token for key. When none is left it returns rate_limited with the time until
// the next one, and takes nothing.
func (l *limiter) allow(key string) error {
	_, err := l.reserve(key, time.Now())
	return err
}

// reserve takes one token for key and returns the reservation, so a caller holding several can
// cancel them together. When no token is available now, it cancels its own reservation, takes
// nothing, and returns rate_limited with the wait.
func (l *limiter) reserve(key string, now time.Time) (*rate.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > time.Minute {
		l.sweep(now)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	res := b.lim.ReserveN(now, 1)
	if !res.OK() {
		return nil, apierr.New(apierr.RateLimited, "too many attempts; try again later").Retry(time.Minute)
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return nil, apierr.New(apierr.RateLimited, "too many attempts; try again later").Retry(delay)
	}
	return res, nil
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

// charge names one limiter and the key to charge in it.
type charge struct {
	name string // the limiter's name, as in Server.limits
	key  string
}

// chargeAll takes one token from each named bucket, or from none. If any bucket is empty, the tokens
// already taken are given back, so a refused request spends nothing from the others.
func (s *Server) chargeAll(charges ...charge) error {
	now := time.Now()
	var held []*rate.Reservation
	for _, c := range charges {
		res, err := s.limits[c.name].reserve(c.key, now)
		if err != nil {
			for _, h := range held {
				h.CancelAt(now)
			}
			return err
		}
		held = append(held, res)
	}
	return nil
}
