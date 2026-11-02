// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"
)

// A file's tags are shown only from a ready read under the current converter. A values row left by an older converter, or
// by a read that is not ready, is not shown: the routes and the listings must agree (decision 56).
func TestAudioTagsAreShownOnlyForTheCurrentConverter(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	now := time.UnixMilli(1_000)
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertBlob(ctx, &Blob{ID: "blb_a", AccountID: "acc_a", Size: 10, SHA256: "00", State: BlobLive,
			CreatedAt: now}); err != nil {
			return err
		}
		n := node("nod_a", "fld_a", "nod_root", "song.mp3", "file")
		n.BlobID = "blb_a"
		if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
			return err
		}
		if _, err := tx.InsertDerivedPending(ctx, &Derived{ID: "drv_old", AccountID: "acc_a", BlobID: "blb_a",
			Kind: AudioTagsKind, Converter: "old", CreatedAt: now}); err != nil {
			return err
		}
		if _, err := tx.CompleteDerived(ctx, "acc_a", "drv_old", 0, "", now); err != nil {
			return err
		}
		return tx.UpsertAudioTags(ctx, "acc_a", "blb_a", AudioTags{Title: "Old read", DurationMS: 5})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AudioTagsFor(ctx, "acc_a", []string{"nod_a"}, "old")
	if err != nil || got["nod_a"].Title != "Old read" {
		t.Fatalf("under its own converter: %+v %v, want the values", got, err)
	}
	got, err = s.AudioTagsFor(ctx, "acc_a", []string{"nod_a"}, "new")
	if err != nil || len(got) != 0 {
		t.Fatalf("under a newer converter: %+v %v, want no values (the route says pending and listings must too)", got, err)
	}
}
