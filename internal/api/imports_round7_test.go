// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// stagedFiles starts an import of data into parent (or the folder, when parent is ""), with on_conflict policy, and
// runs its job up to the publish, where it yields. The files are staged and nothing is published. It returns the import.
func stagedFiles(t *testing.T, fx *importFixture, folder, parent string, data []byte, policy string) importJSON {
	t.Helper()
	body := map[string]any{"source": "archive", "folder": folder, "upload": fx.archive(t, "a.zip", data)}
	if parent != "" {
		body["parent"] = parent
	}
	if policy != "" {
		body["on_conflict"] = policy
	}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusAccepted)
	imp := decode[importJSON](t, rec)
	yielded := false
	fx.s.importFault = func(stage string) error {
		if stage == "publish" && !yielded {
			yielded = true
			return jobs.ErrYield
		}
		return nil
	}
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("the run did not yield at the publish: %v", err)
	}
	fx.s.importFault = nil
	return imp
}

// Removing the folder an import is staged into takes no usage with it: the staged bytes were never counted, and the live
// files of the account keep their usage (round 7, B1). Before, the staging folder's delete subtracted the staged bytes.
func TestRemovingTheTargetKeepsTheUsageOfLiveFiles(t *testing.T) {
	fx := newImportFixture(t)
	_ = importInto(t, fx, "", zipArchive(t, [][2]string{{"keep.bin", strings.Repeat("k", 500)}}))
	live := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes
	if live != 500 {
		t.Fatalf("the live file uses %d bytes, want 500", live)
	}
	team := newFolder(t, fx.s, fx.key, fx.acc.ID, "Team")
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+root.ID+"/permissions/"+fx.user.ID, fx.key,
		map[string]any{"role": store.RoleEditor}), http.StatusOK)
	_ = stagedFiles(t, fx, team.ID, "", zipArchive(t, [][2]string{{"big.bin", strings.Repeat("b", 3000)}}), "")
	if got := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes; got != live {
		t.Fatalf("staging changed the usage to %d, want %d (staged bytes are not usage until publish)", got, live)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+team.ID, fx.key, nil), http.StatusNoContent)
	u := usageOf(t, fx.s, fx.key, fx.acc.ID)
	if u.UsedBytes != live {
		t.Fatalf("removing the target left the usage at %d, want %d", u.UsedBytes, live)
	}
	if u.ReservedBytes != 0 {
		t.Fatalf("reserved after the removal = %d, want 0", u.ReservedBytes)
	}
	if n := stagingFoldersOf(t, fx); n != 0 {
		t.Fatalf("%d staging folders left after the removal", n)
	}
}

// A re-import of an archive under a directory an earlier import made meets no clash with its own removed file: the file is
// placed under the reused directory with its name, whatever the policy (round 7, B2).
func TestReimportUnderAReusedDirectoryKeepsItsNames(t *testing.T) {
	for _, policy := range []string{"rename", "replace", "fail"} {
		t.Run(policy, func(t *testing.T) {
			fx := newImportFixture(t)
			data := zipArchive(t, [][2]string{{"t/a.txt", "one"}})
			target := mkdirIn(t, fx, fx.folder, "", "D")
			_ = importInto(t, fx, target.ID, data)
			reused := childrenOfNode(t, fx, target.ID)["t"]
			a := childrenOfNode(t, fx, reused.ID)["a.txt"]
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), http.StatusNoContent)
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), http.StatusNoContent)
			imp := importInto2(t, fx, target.ID, data, policy)
			if got := fx.get(t, imp.ID); got.State != store.ImportDone {
				t.Fatalf("the re-import is %s (%q), want done", got.State, got.ErrorCode)
			}
			kids := childrenOfNode(t, fx, reused.ID)
			if _, ok := kids["a.txt"]; !ok || len(kids) != 1 {
				names := []string{}
				for n := range kids {
					names = append(names, n)
				}
				t.Fatalf("the directory holds %v, want exactly a.txt", names)
			}
		})
	}
}

// A live file of the same name, put there by another import, is handled by the policy: rename keeps both (the new one is
// renamed), replace takes the new file's place, fail refuses the import (round 7, B2).
func TestReimportClashesWithALiveFileByPolicy(t *testing.T) {
	for _, policy := range []string{"rename", "replace", "fail"} {
		t.Run(policy, func(t *testing.T) {
			fx := newImportFixture(t)
			data := zipArchive(t, [][2]string{{"t/a.txt", "one"}})
			target := mkdirIn(t, fx, fx.folder, "", "D")
			_ = importInto(t, fx, target.ID, data)
			reused := childrenOfNode(t, fx, target.ID)["t"]
			a := childrenOfNode(t, fx, reused.ID)["a.txt"]
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), http.StatusNoContent)
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), http.StatusNoContent)
			// Another import puts a live a.txt of other content into the directory.
			_ = importInto(t, fx, reused.ID, zipArchive(t, [][2]string{{"a.txt", "two-two"}}))
			imp := importInto2(t, fx, target.ID, data, policy)
			got := fx.get(t, imp.ID)
			kids := childrenOfNode(t, fx, reused.ID)
			switch policy {
			case "fail":
				if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Conflict) {
					t.Fatalf("fail: state %s code %q, want failed with conflict", got.State, got.ErrorCode)
				}
			case "replace":
				if got.State != store.ImportDone || len(kids) != 1 || kids["a.txt"].Size != 3 {
					t.Fatalf("replace: state %s, %d children, a.txt size %d; want done with the new file alone", got.State, len(kids), kids["a.txt"].Size)
				}
			case "rename":
				if got.State != store.ImportDone || len(kids) != 2 || kids["a.txt"].Size != 7 {
					t.Fatalf("rename: state %s, %d children, a.txt size %d; want done with both files", got.State, len(kids), kids["a.txt"].Size)
				}
			}
		})
	}
}

// importInto2 asks for an import of data into parent with the on_conflict policy, runs it to the end, and returns it.
func importInto2(t *testing.T, fx *importFixture, parent string, data []byte, policy string) importJSON {
	t.Helper()
	body := map[string]any{"source": "archive", "folder": fx.folder, "parent": parent, "on_conflict": policy,
		"upload": fx.archive(t, "a.zip", data)}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusAccepted)
	imp := decode[importJSON](t, rec)
	_ = fx.runJob(t) // a failed import's job reports its code; the record says what happened
	return imp
}

// Pausing an import after it has staged its files keeps its declared size against the quota: its staged bytes are not in
// the usage until publish, so a second import is refused at its scan (round 7, B4).
func TestPausedStagedImportStillHoldsItsQuota(t *testing.T) {
	fx := newImportFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 1000}), http.StatusOK)
	a := stagedFiles(t, fx, fx.folder, "", zipArchive(t, [][2]string{{"a.bin", strings.Repeat("0", 600)}}), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+a.ID+"/pause", nil), http.StatusOK)
	_ = fx.runJob(t) // the paused import's job ends
	b := fx.startOK(t, fx.archive(t, "b.zip", zipArchive(t, [][2]string{{"b.bin", strings.Repeat("0", 600)}})), "")
	_ = fx.runJob(t)
	got := fx.get(t, b.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.QuotaExceeded) {
		t.Fatalf("the second import: state %s code %q, want failed with quota_exceeded", got.State, got.ErrorCode)
	}
}

// A removal of the target while the import runs ends the job quietly: the import is gone, so there is no failure to report
// (round 7, B5). Before, the job reported internal.
func TestTargetRemovedMidRunEndsTheJobQuietly(t *testing.T) {
	fx := newImportFixture(t)
	team := newFolder(t, fx.s, fx.key, fx.acc.ID, "Team")
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+root.ID+"/permissions/"+fx.user.ID, fx.key,
		map[string]any{"role": store.RoleEditor}), http.StatusOK)
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", map[string]any{"source": "archive", "folder": team.ID,
		"upload": fx.archive(t, "a.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))})
	wantStatus(t, rec, http.StatusAccepted)
	fx.s.importFault = func(stage string) error {
		if stage == "publish" {
			// The target goes while the job runs: its import goes with it, by the cascade.
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+team.ID, fx.key, nil), http.StatusNoContent)
		}
		return nil
	}
	err = fx.runJob(t)
	fx.s.importFault = nil
	if err != nil {
		t.Fatalf("the job of a removed import reported %v, want no error", err)
	}
}

// A poison import whose requeues are used up is failed and cleaned: its reservations are released and its staging is
// gone, so it holds no quota (round 7, B6). Before, it kept its archive and reservation for ever.
func TestPoisonImportEndsItsCleanup(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importFault = func(stage string) error {
		switch stage {
		case "stage":
			return errors.New("the stage broke")
		case "cleanup":
			return errors.New("the cleanup broke")
		}
		return nil
	}
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	_ = fx.runJob(t)
	for i := 0; i < 6; i++ {
		n, err := fx.s.requeueImports(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		_ = fx.runJob(t)
	}
	fx.s.importFault = nil
	if got := reservedOf(t, fx); got != 0 {
		t.Fatalf("the poison import still holds %d bytes of quota", got)
	}
	if n := stagingFoldersOf(t, fx); n != 0 {
		t.Fatalf("%d staging folders left by the poison import", n)
	}
	row, err := fx.s.store.Import(context.Background(), imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Phase != store.ImportPhaseDone {
		t.Fatalf("the poison import is in phase %s, want done", row.Phase)
	}
}
