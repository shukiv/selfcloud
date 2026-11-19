// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// setCreatedAt sets a node's created time to an exact instant, in unix milliseconds.
func setCreatedAt(t *testing.T, s *Server, id string, at int64) {
	t.Helper()
	err := s.store.Write(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Querier().ExecContext(t.Context(), "UPDATE nodes SET created_at = ? WHERE id = ?", at, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSearchCreatedParams: created_after and created_before are whole milliseconds, zero or more, and the range runs
// forward: after is inclusive and before exclusive, so equal ends are refused. Each bad value names its parameter.
func TestSearchCreatedParams(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	for _, c := range []struct{ query, message string }{
		{"created_after=soon", "created_after must be a whole number, zero or more"},
		{"created_after=-1", "created_after must be a whole number, zero or more"},
		{"created_before=1.5", "created_before must be a whole number, zero or more"},
		{"created_before=-2", "created_before must be a whole number, zero or more"},
		{"created_after=5&created_before=5", "created_after must be before created_before"},
		{"created_after=6&created_before=5", "created_after must be before created_before"},
		{"q=report&created_after=9&created_before=8", "created_after must be before created_before"},
	} {
		rec := fx.search(t, c.query)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s: %d %s; want 400 naming %q", c.query, rec.Code, rec.Body, c.message)
		}
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	// A created bound alone is a filter: the search runs without a query. Zero is a valid bound.
	for _, query := range []string{"created_after=0", "created_before=0", "created_after=1&created_before=2"} {
		wantStatus(t, fx.search(t, query), 200)
	}
}

// TestSearchCreatedFilterBounds: a name search keeps a node created at exactly created_after and drops one created at
// exactly created_before, the half-open range the modified bounds use.
func TestSearchCreatedFilterBounds(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	lo := putTyped(t, fx.s, fx.key, fx.fld.ID, "created-lo.txt", "text/plain", "x").ID
	hi := putTyped(t, fx.s, fx.key, fx.fld.ID, "created-hi.txt", "text/plain", "x").ID
	setCreatedAt(t, fx.s, lo, boundaryAfter)
	setCreatedAt(t, fx.s, hi, boundaryBefore)
	after := strconv.FormatInt(boundaryAfter, 10)
	before := strconv.FormatInt(boundaryBefore, 10)
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"q=created&created_after=" + after + "&created_before=" + before, []string{"created-lo.txt"}},
		{"q=created&created_after=" + before, []string{"created-hi.txt"}},
		{"q=created&created_before=" + after, nil},
		{"created_after=" + after + "&created_before=" + before + "&kind=file", []string{"created-lo.txt"}},
	} {
		got := hitNames(t, fx.search(t, c.query))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.query, got, c.want)
		}
	}
}

// TestSearchCreatedFilterContentBounds: the content search applies the same half-open created range to the node.
func TestSearchCreatedFilterContentBounds(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	lo := fx.file(t, "created-lo.txt", "createdword").ID
	hi := fx.file(t, "created-hi.txt", "createdword").ID
	drainText(t, fx.s.textWorker)
	setCreatedAt(t, fx.s, lo, boundaryAfter)
	setCreatedAt(t, fx.s, hi, boundaryBefore)
	after := strconv.FormatInt(boundaryAfter, 10)
	before := strconv.FormatInt(boundaryBefore, 10)
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"created_after=" + after + "&created_before=" + before, []string{"created-lo.txt"}},
		{"created_after=" + before, []string{"created-hi.txt"}},
		{"created_before=" + after, nil},
	} {
		rec := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/search?in=content&q=createdword&"+c.query, nil)
		wantStatus(t, rec, 200)
		got := contentNames(decode[testPage](t, rec).Items)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %q, want %q", c.query, got, c.want)
		}
	}
}
