// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
)

// TestRefundTwiceRestoresOnce: two holds take two of the three notice tokens of one address. Refunding the first hold twice
// must restore its token once, so afterwards exactly two more charges for that address succeed and the third is refused. A
// second cancel with no flag would restore it again and allow three (red when the spent check is removed).
func TestRefundTwiceRestoresOnce(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	s.limitClock = frozenLimitClock
	const acc, addr = "acc_hold", "old@example.com"
	h1, err := s.reserveNotice(acc, addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveNotice(acc, addr); err != nil {
		t.Fatal(err)
	}
	s.refund(h1)
	s.refund(h1)
	n := 0
	for n < 10 {
		if _, err := s.reserveNotice(acc, addr); err != nil {
			break
		}
		n++
	}
	if n != 2 {
		t.Errorf("after refunding one hold twice, %d more charges succeeded, want 2 (its token restored once)", n)
	}
}
