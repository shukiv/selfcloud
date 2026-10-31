// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// The delivery schedule (design section 10, decision 28).
const (
	// BackoffFirst is the wait after the first failed attempt. Each later wait doubles, up to BackoffCap.
	BackoffFirst = 30 * time.Second
	// BackoffCap is the longest base wait.
	BackoffCap = time.Hour
	// GiveUpAfter is how long after its first attempt a delivery is given up, and no attempt is made past it.
	GiveUpAfter = 24 * time.Hour
	// BackoffJitter is the most a wait is lengthened by, as a share of the base wait.
	BackoffJitter = 0.2
)

// Signature returns the Selfcloud-Signature header for body, signed at t with each secret, in order: "t=<unix
// seconds>,v1=<hex>", with one v1 for each secret. Two v1 values appear while a rotation's old secret is in its grace
// period. The signed text is "<t>.<body>", where body is the bytes sent.
func Signature(secrets [][]byte, t time.Time, body []byte) string {
	unix := t.Unix()
	var b strings.Builder
	fmt.Fprintf(&b, "t=%d", unix)
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, secret)
		fmt.Fprintf(mac, "%d.", unix)
		mac.Write(body)
		fmt.Fprintf(&b, ",v1=%s", hex.EncodeToString(mac.Sum(nil)))
	}
	return b.String()
}

// Backoff returns the wait before the next attempt, after attempts attempts have failed (1 after the first). The base
// is BackoffFirst doubled once per further failure, and at most BackoffCap. Jitter lengthens it by up to
// BackoffJitter of the base, so the longest wait is 72 minutes. jitter is a number in [0, 1); the caller draws it.
func Backoff(attempts int, jitter float64) time.Duration {
	base := BackoffFirst
	for i := 1; i < attempts && base < BackoffCap; i++ {
		base *= 2
	}
	if base > BackoffCap {
		base = BackoffCap
	}
	return base + time.Duration(float64(base)*BackoffJitter*jitter)
}

// Jitter draws a number in [0, 1) for Backoff.
func Jitter() float64 { return rand.Float64() }
