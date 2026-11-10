// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// The range bounds of a file list: modified_after is inclusive and modified_before is exclusive (decision 110). The
// boundary files sit at exactly those instants, so an inclusive end or an exclusive start is seen on the list.
const (
	boundaryAfter  int64 = 1_700_000_000_000
	boundaryBefore       = boundaryAfter + 3_600_000
)

// setModifiedAt sets a node's modified time to an exact instant, in unix milliseconds.
func setModifiedAt(t *testing.T, s *Server, id string, at int64) {
	t.Helper()
	err := s.store.Write(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Querier().ExecContext(t.Context(), "UPDATE nodes SET modified_at = ? WHERE id = ?", at, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// boundaryNodes makes two files in folder: boundary-lo.pdf modified at exactly modified_after, and boundary-hi.pdf at
// exactly modified_before. It returns their IDs.
func boundaryNodes(t *testing.T, s *Server, key, folder string) (lo, hi string) {
	t.Helper()
	lo = putNode(t, s, key, folder, "/boundary-lo.pdf", "xx").ID
	hi = putNode(t, s, key, folder, "/boundary-hi.pdf", "xx").ID
	setModifiedAt(t, s, lo, boundaryAfter)
	setModifiedAt(t, s, hi, boundaryBefore)
	return lo, hi
}

// boundaryCase sets up one list and asks it for the range q; it returns the status and the body.
type boundaryCase struct {
	name string
	run  func(t *testing.T, fx *p1Fixture, q string) (int, string)
}

var boundaryCases = []boundaryCase{
	{"largest, the caller's own folder (/me)", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		boundaryNodes(t, fx.s, fx.key, fx.annPF)
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/me/storage/largest?"+q, nil)
		return rec.Code, rec.Body.String()
	}},
	{"largest, the account", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		boundaryNodes(t, fx.s, fx.key, fx.annPF)
		rec := call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/storage/largest?"+q, fx.key, nil)
		return rec.Code, rec.Body.String()
	}},
	{"trash", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		lo, hi := boundaryNodes(t, fx.s, fx.key, fx.fld.ID)
		for _, id := range []string{lo, hi} {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+id, fx.key, nil), 204)
		}
		rec := call(t, fx.s, "GET", "/api/v1/trash?"+q, fx.key, nil)
		return rec.Code, rec.Body.String()
	}},
	{"recent", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		lo, hi := boundaryNodes(t, fx.s, fx.key, fx.annPF)
		if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
			for _, id := range []string{lo, hi} {
				if err := tx.RecordEdit(t.Context(), fx.acc.ID, fx.ann.ID, id, time.Now()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/recent?"+q, nil)
		return rec.Code, rec.Body.String()
	}},
	{"starred", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		lo, hi := boundaryNodes(t, fx.s, fx.key, fx.annPF)
		for _, id := range []string{lo, hi} {
			wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+id+"/star", nil), 204)
		}
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/starred?"+q, nil)
		return rec.Code, rec.Body.String()
	}},
	{"shared", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		lo, hi := boundaryNodes(t, fx.s, fx.key, fx.fld.ID)
		fx.setPerm(t, lo, fx.ann.ID, "viewer")
		fx.setPerm(t, hi, fx.ann.ID, "viewer")
		rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared?"+q, nil)
		return rec.Code, rec.Body.String()
	}},
	{"search", func(t *testing.T, fx *p1Fixture, q string) (int, string) {
		boundaryNodes(t, fx.s, fx.key, fx.fld.ID)
		rec := call(t, fx.s, "GET", "/api/v1/search?q=boundary&"+q, fx.key, nil)
		return rec.Code, rec.Body.String()
	}},
}

// TestModifiedRangeIsHalfOpenOnEveryList: on each file list and on both Largest routes, a file modified at exactly
// modified_after is listed and a file modified at exactly modified_before is not (review L4 of ff1a, decision 110).
func TestModifiedRangeIsHalfOpenOnEveryList(t *testing.T) {
	q := "modified_after=" + strconv.FormatInt(boundaryAfter, 10) + "&modified_before=" + strconv.FormatInt(boundaryBefore, 10)
	for _, c := range boundaryCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := newP1Fixture(t)
			code, body := c.run(t, fx, q)
			if code != 200 {
				t.Fatalf("%s: status %d, want 200: %s", c.name, code, body)
			}
			if !strings.Contains(body, "boundary-lo.pdf") {
				t.Errorf("%s: the file modified at modified_after is not listed: %s", c.name, body)
			}
			if strings.Contains(body, "boundary-hi.pdf") {
				t.Errorf("%s: the file modified at modified_before is listed: %s", c.name, body)
			}
		})
	}
}
