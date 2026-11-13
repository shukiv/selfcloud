// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// mkdirIn makes a directory named name in the folder, under parent (the folder's root when parent is ""), and returns it.
func mkdirIn(t *testing.T, fx *importFixture, folder, parent, name string) nodeJSON {
	t.Helper()
	// A name goes with a parent; without one the directory is given by its path from the folder's root.
	body := map[string]any{"path": name}
	if parent != "" {
		body = map[string]any{"name": name, "parent": parent}
	}
	rec := call(t, fx.s, "POST", "/api/v1/folders/"+folder+"/directories", fx.key, body)
	if rec.Code/100 != 2 {
		t.Fatalf("mkdir %s = %d: %s", name, rec.Code, rec.Body.String())
	}
	return decode[nodeJSON](t, rec)
}

// childrenOfNode lists the children of a directory, by name.
func childrenOfNode(t *testing.T, fx *importFixture, dir string) map[string]nodeJSON {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/nodes/"+dir+"/children", fx.key, nil)
	wantStatus(t, rec, http.StatusOK)
	out := map[string]nodeJSON{}
	for _, n := range decode[listResponse[nodeJSON]](t, rec).Items {
		out[n.Name] = n
	}
	return out
}

// A directory reused by an import must lie inside the import's target directory at publish. Moved out of the target,
// into the folder's root or under a sibling, it stays in the same folder, so only the walk up to the target shows it
// (containment).
func TestImportPublishRefusesAReusedDirectoryMovedOutOfTheTarget(t *testing.T) {
	for _, dest := range []string{"root", "sibling"} {
		t.Run(dest, func(t *testing.T) {
			fx := newImportFixture(t)
			data := zipArchive(t, [][2]string{{"t/a.txt", "1"}})
			target := mkdirIn(t, fx, fx.folder, "", "D")
			_ = importInto(t, fx, target.ID, data)
			reused := childrenOfNode(t, fx, target.ID)["t"]
			// The file is removed, so the second import writes it again under the reused directory.
			kids := childrenOfNode(t, fx, reused.ID)
			a := kids["a.txt"]
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), http.StatusNoContent)
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), http.StatusNoContent)
			yielded := false
			fx.s.importFault = func(stage string) error {
				if stage == "publish" && !yielded {
					yielded = true
					return jobs.ErrYield
				}
				return nil
			}
			body := map[string]any{"source": "archive", "folder": fx.folder, "parent": target.ID,
				"upload": fx.archive(t, "t.zip", data)}
			rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
			wantStatus(t, rec, http.StatusAccepted)
			imp := decode[importJSON](t, rec)
			_ = fx.runJob(t) // yields at publish
			// A move names the directory it goes into: the folder's root, or a sibling of the target.
			root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, fx.folder)
			if err != nil {
				t.Fatal(err)
			}
			moveBody := map[string]any{"folder": fx.folder, "parent": root.ID}
			if dest == "sibling" {
				s := mkdirIn(t, fx, fx.folder, "", "S")
				moveBody["parent"] = s.ID
			}
			wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+reused.ID+"/move", fx.key, moveBody), http.StatusOK)
			_ = fx.runJob(t)
			fx.s.importFault = nil
			got := fx.get(t, imp.ID)
			if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Conflict) {
				t.Fatalf("after the move to the %s: state %s code %q, want failed with conflict", dest, got.State, got.ErrorCode)
			}
		})
	}
}

// A second request for an archive that another request is turning into an import waits for it, and answers locked (409)
// while the first is in flight; once the first is done the archive is gone and the answer is not_found.
func TestSecondImportRequestForAnArchiveIsLockedNotFailed(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.uploadSlotWait = 20 * time.Millisecond
	upl := fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))
	release, err := fx.s.takeUploadSlot(httptest.NewRecorder(), upl)
	if err != nil {
		t.Fatalf("taking the archive's slot: %v", err)
	}
	rec := fx.start(t, upl, "")
	wantStatus(t, rec, http.StatusConflict)
	if got := decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, rec); got.Error.Code != string(apierr.Locked) {
		t.Fatalf("the second request answered %q, want locked", got.Error.Code)
	}
	release()
	first := fx.start(t, upl, "")
	wantStatus(t, first, http.StatusAccepted)
	wantStatus(t, fx.start(t, upl, ""), http.StatusNotFound)
}

// importInto asks for an import of the archive data into parent, and runs it to the end.
func importInto(t *testing.T, fx *importFixture, parent string, data []byte) importJSON {
	t.Helper()
	body := map[string]any{"source": "archive", "folder": fx.folder, "parent": parent,
		"upload": fx.archive(t, "a.zip", data)}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusAccepted)
	imp := decode[importJSON](t, rec)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("import: %v", err)
	}
	return imp
}
