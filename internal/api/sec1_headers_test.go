// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shukiv/selfcloud/internal/mail"
)

// sec1 item 7: a JSON answer is not cached and is not sniffed, as an error already is.
func TestWriteJSONSetsNoStoreAndNosniff(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"a": "b"})
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// sec1 item 7: a handler that set its own cache header keeps it; writeJSON does not overwrite it.
func TestWriteJSONKeepsACacheHeaderTheHandlerSet(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	rec.Header().Set("Cache-Control", "private, no-cache")
	writeJSON(rec, http.StatusOK, map[string]string{"a": "b"})
	if got := rec.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control = %q, want the handler's private, no-cache", got)
	}
}

// sec1 item 7: a signed-in answer from a real route carries the headers too.
func TestMeAnswerIsNotCachedAndNotSniffed(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	rec := callSession(t, s, sess, "GET", "/api/v1/me", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("/me Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("/me X-Content-Type-Options = %q, want nosniff", got)
	}
}
