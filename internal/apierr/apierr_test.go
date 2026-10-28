// SPDX-License-Identifier: AGPL-3.0-or-later

package apierr

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestEveryCodeHasAStatus(t *testing.T) {
	for _, c := range Codes() {
		if s := c.Status(); s < 400 || s > 599 {
			t.Errorf("%s maps to status %d", c, s)
		}
	}
	if got := Code("no_such_code").Status(); got != http.StatusInternalServerError {
		t.Errorf("unknown code maps to %d, want 500", got)
	}
}

func TestFrom(t *testing.T) {
	coded := New(QuotaExceeded, "full").With("required_bytes", 10)
	wrapped := fmt.Errorf("upload: %w", coded)
	if got := From(wrapped); got != coded {
		t.Errorf("From lost the coded error inside a wrapped one: %v", got)
	}

	got := From(io.ErrUnexpectedEOF)
	if got.Code != Internal || got.Message != "internal error" {
		t.Errorf("From(plain error) = %+v, want an internal error with a generic message", got)
	}
	if !errors.Is(got, io.ErrUnexpectedEOF) {
		t.Error("From(plain error) dropped the cause")
	}
}
