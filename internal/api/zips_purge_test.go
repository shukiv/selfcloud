// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"
)

// fu2 item 8 (decision 35). A zip is a snapshot of the tree, and it reads each file from the blob the snapshot named. A
// purge that removes a file's blob after the snapshot and before that file's entry is written cannot be read: the
// entry fails, and the whole zip fails with it. The decision states that the zip keeps a node trashed or deleted after
// the snapshot, and this test pins the divergence: a purged blob is not kept, and the job fails rather than leave the
// file out.
func TestZipFailsWhenAFilesBlobIsPurgedBeforeItsEntry(t *testing.T) {
	fx := zipFixture(t)
	node, err := fx.s.store.Node(context.Background(), fx.acc.ID, fx.deep.ID)
	if err != nil {
		t.Fatal(err)
	}
	job := createZip(t, fx, nil, fx.a.ID)
	// The first entry's turn comes after the snapshot, so the purge lands between the snapshot and the file's entry.
	purged := false
	fx.s.zipFault = func(stage string) error {
		if stage == "entry" && !purged {
			purged = true
			if err := fx.s.blobs.Remove(fx.acc.ID, node.BlobID); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	j := runZip(t, fx, job.ID)
	if !purged {
		t.Fatal("the purge did not happen")
	}
	if j.Status != "failed" || !strings.Contains(j.Error, "contents are missing") {
		t.Fatalf("job %s %q, want failed because the file's contents are missing", j.Status, j.Error)
	}
	if rec := getZip(t, fx, nil, job.Target, nil); rec.Code == 200 {
		t.Fatal("a zip with a purged file was published")
	}
}
