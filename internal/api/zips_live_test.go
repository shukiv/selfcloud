// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// assertZipRefused checks that a zip was refused: its row failed, its file is gone and it holds no disk budget.
func assertZipRefused(t *testing.T, fx *sharingFixture, zipID string) {
	t.Helper()
	ctx := context.Background()
	z, err := fx.s.store.Zip(ctx, fx.acc.ID, zipID)
	if err != nil || z.State != store.ZipFailed {
		t.Fatalf("zip after the refusal: %v, %+v; want failed", err, z)
	}
	if _, reserved, err := fx.s.store.ZipDiskUse(ctx); err != nil || reserved != 0 {
		t.Errorf("reserved after the refusal = %d, %v; want 0", reserved, err)
	}
	final, _ := fx.s.zipFiles.Path(fx.acc.ID, zipID)
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Errorf("zip file after the refusal: %v; want none", err)
	}
}

// TestZipJobRefusesARevokedToken: a zip made by a token is refused, and makes no file, once the user's tokens are
// revoked before the job runs. Copy jobs check this (copyActorLive); a zip must too (review finding).
func TestZipJobRefusesARevokedToken(t *testing.T) {
	fx := zipFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	job := createZipWithCred(t, fx, tok, fx.a.ID)
	wantStatus(t, call(t, fx.s, "POST", userRevokePath(fx.acc.ID, fx.ann.ID), fx.key, nil), 200)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "no longer has access") {
		t.Fatalf("job %s %q; want failed on the caller's access", j.Status, j.Error)
	}
	assertZipRefused(t, fx, job.Target)
}

// TestZipJobRefusesARevokeBeforePublish: a revoke that lands after the last entry and the last turn's check is still
// caught, by the check inside the publish write.
func TestZipJobRefusesARevokeBeforePublish(t *testing.T) {
	fx := zipFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	job := createZipWithCred(t, fx, tok, fx.a.ID)
	fx.s.zipFault = func(stage string) error {
		if stage == "publish" {
			wantStatus(t, call(t, fx.s, "POST", userRevokePath(fx.acc.ID, fx.ann.ID), fx.key, nil), 200)
		}
		return nil
	}
	j := runZip(t, fx, job.ID)
	fx.s.zipFault = nil
	if j.Status != "failed" || !strings.Contains(j.Error, "no longer has access") {
		t.Fatalf("job %s %q; want failed on the caller's access", j.Status, j.Error)
	}
	assertZipRefused(t, fx, job.Target)
}

// TestZipJobRefusesARevokeBetweenTurns: the job yields after each entry, and a revoke during a turn stops the next
// one. The check at each turn's end is the one that sees it.
func TestZipJobRefusesARevokeBetweenTurns(t *testing.T) {
	fx := zipFixture(t)
	fx.s.zipYield = time.Nanosecond
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	job := createZipWithCred(t, fx, tok, fx.a.ID)
	revoked := false
	entriesAfter := 0 // entries written after the revoke: the turn check must stop the job before the next one
	fx.s.zipFault = func(stage string) error {
		if stage != "entry" {
			return nil
		}
		if revoked {
			entriesAfter++
		} else {
			revoked = true
			wantStatus(t, call(t, fx.s, "POST", userRevokePath(fx.acc.ID, fx.ann.ID), fx.key, nil), 200)
		}
		return nil
	}
	j := runZip(t, fx, job.ID)
	for i := 0; i < 20 && j.Status == "queued"; i++ {
		j = runZip(t, fx, job.ID)
	}
	fx.s.zipFault = nil
	fx.s.zipYield = 0
	if j.Status != "failed" || !strings.Contains(j.Error, "no longer has access") {
		t.Fatalf("job %s %q after a revoke between turns; want failed", j.Status, j.Error)
	}
	if entriesAfter != 0 {
		t.Errorf("%d entries written after the revoke; the turn check should stop the job at once", entriesAfter)
	}
	assertZipRefused(t, fx, job.Target)
}

// bulkDirs adds n folders under parent in one write, through the store.
func bulkDirs(t *testing.T, fx *sharingFixture, parentID string, n int) {
	t.Helper()
	parent, err := fx.s.store.Node(t.Context(), fx.acc.ID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
		for i := 0; i < n; i++ {
			name := "d" + strconv.Itoa(i)
			nd := &store.Node{ID: id.New("nod"), AccountID: fx.acc.ID, FolderID: fx.fld.ID, ParentID: parent.ID,
				Name: name, NameKey: name, Kind: store.KindDir, CreatedAt: now, ModifiedAt: now,
				CreatedBy: parent.CreatedBy, ModifiedBy: parent.ModifiedBy}
			if err := tx.InsertNode(t.Context(), nd); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestZipTreeReadsAtMostTheLimit: the subtree query returns at most limit rows, however many nodes are under the root.
// The limit is in the query, so a huge subtree costs the limit in memory. The mutation is a query without its LIMIT: the
// count then is the whole subtree.
func TestZipTreeReadsAtMostTheLimit(t *testing.T) {
	fx := zipFixture(t)
	bulkDirs(t, fx, fx.b.ID, 3000)
	got, err := fx.s.store.ZipTree(context.Background(), fx.acc.ID, fx.a.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("ZipTree rows = %d, want 4: the query read past its limit", len(got))
	}
}

// TestZipLimitIsARunningTotalAcrossRoots: each root fits the limit, the total does not, and the zip is refused with the
// too-large error before anything is built. The reads stay bounded by the limit plus one (TestZipTreeReadsAtMostTheLimit).
func TestZipLimitIsARunningTotalAcrossRoots(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) { c.Zip.MaxEntries = 3 })
	// b holds b and file.txt; c holds c and x.txt. Each is 2 nodes, together 4.
	job := createZip(t, fx, nil, fx.b.ID, fx.c.ID)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "at most 3 entries") {
		t.Fatalf("job %s %q; want failed on the entry limit across roots", j.Status, j.Error)
	}
	assertZipRefused(t, fx, job.Target)
}

// TestZipLimitCountsNodesSkippedForAccess: a node left out because the caller cannot read it still counts against the
// limit. Here c and its file are left out, and the four nodes that remain would fit in 5, but the six read do not.
func TestZipLimitCountsNodesSkippedForAccess(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) { c.Zip.MaxEntries = 5 })
	fx.s.zipWalkFault = func(nodeID string) error {
		if nodeID == fx.c.ID {
			return apierr.New(apierr.Forbidden, "not for you")
		}
		return nil
	}
	job := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, job.ID)
	fx.s.zipWalkFault = nil
	if j.Status != "failed" || !strings.Contains(j.Error, "at most 5 entries") {
		t.Fatalf("job %s %q; want failed: the skipped nodes count", j.Status, j.Error)
	}
	assertZipRefused(t, fx, job.Target)
}
