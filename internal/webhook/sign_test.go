// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// The header format is pinned: t, then one v1 per secret, each the HMAC-SHA256 of "<t>.<body>".
func TestSignatureFormatIsPinned(t *testing.T) {
	t.Parallel()
	secret := []byte("0123456789abcdef0123456789abcdef")
	at := time.Unix(1_800_000_000, 999_000_000) // sub-second parts are dropped
	body := []byte(`{"id":"evt_1","type":"file.created"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("1800000000." + string(body)))
	want := "t=1800000000,v1=" + hex.EncodeToString(mac.Sum(nil))
	if got := Signature([][]byte{secret}, at, body); got != want {
		t.Fatalf("signature = %s\nwant %s", got, want)
	}
}

// During a rotation the header carries two v1 values, the current secret first, and each one verifies on its own.
func TestSignatureCarriesEverySecretDuringRotation(t *testing.T) {
	t.Parallel()
	cur, old := []byte("current-secret-current-secret-32"), []byte("old-secret-old-secret-old-secret!")
	body := []byte(`{"data":{}}`)
	at := time.Unix(1_800_000_100, 0)
	header := Signature([][]byte{cur, old}, at, body)
	parts := strings.Split(header, ",")
	if len(parts) != 3 || parts[0] != "t=1800000100" || !strings.HasPrefix(parts[1], "v1=") || !strings.HasPrefix(parts[2], "v1=") {
		t.Fatalf("header = %q, want t then two v1 values", header)
	}
	for i, secret := range [][]byte{cur, old} {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte("1800000100." + string(body)))
		if got := strings.TrimPrefix(parts[i+1], "v1="); !hmac.Equal([]byte(got), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Errorf("v1 %d does not verify with its secret", i)
		}
	}
}

// A retry is signed afresh: its t is the attempt's time, so the same body at a later time has a different header.
func TestRetrySignatureIsFresh(t *testing.T) {
	t.Parallel()
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{}`)
	first := Signature([][]byte{secret}, time.Unix(100, 0), body)
	second := Signature([][]byte{secret}, time.Unix(131, 0), body)
	if first == second {
		t.Fatal("a retry carries the first attempt's signature")
	}
}

// The schedule: 30 s, doubling to a cap of one hour. Jitter lengthens the base by up to 20 percent of it.
func TestBackoffSchedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour, time.Hour,
	}
	for i, base := range want {
		attempts := i + 1
		if got := Backoff(attempts, 0); got != base {
			t.Errorf("after %d failures, wait %v with no jitter, want %v", attempts, got, base)
		}
		if got := Backoff(attempts, 0.999999); got < base || got > base+base/5 {
			t.Errorf("after %d failures, jittered wait %v outside [%v, %v]", attempts, got, base, base+base/5)
		}
	}
	if got := Backoff(1000, 0); got != BackoffCap {
		t.Errorf("a very late retry waits %v, want the cap", got)
	}
}

// The give-up is 24 hours from the first attempt.
func TestGiveUpWindow(t *testing.T) {
	t.Parallel()
	if GiveUpAfter != 24*time.Hour || BackoffFirst != 30*time.Second || BackoffCap != time.Hour {
		t.Fatalf("schedule constants are %v, %v, %v", GiveUpAfter, BackoffFirst, BackoffCap)
	}
}
