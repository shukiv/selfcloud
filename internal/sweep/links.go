// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// linkGrace is how long an ended share link keeps its row. The row is kept a day so the audit shows when the link
// ended, as an expired permission's is; the link answers "not found" from the moment it ends (design section 8).
const linkGrace = 24 * time.Hour

// linkBatch is how many ended links one round looks at.
const linkBatch = 200

// expireShareLinks removes the share links that ended more than linkGrace ago. Each link goes in its own transaction,
// which checks the end again, deletes the link (its visits go with it) and any open uploads a drop link holds, and
// writes the audit entries; the uploads' files are removed once the transaction has committed. A link is about its
// node's folder, so its entries are private as every entry about a personal folder is.
//
// A link that cannot be removed is logged and passed over: the pass goes on to the other links and to the sweeps after
// it, and the link is tried again on the next pass. The pass walks the ended links by their position (end, then ID),
// in batches of linkBatch, so a link that keeps failing never stands in front of the others. Only a cancelled context
// stops it, and that is the only error it returns.
func (w *Sweeper) expireShareLinks(ctx context.Context, now time.Time) (int, error) {
	removed := 0
	cutoff := now.Add(-linkGrace)
	afterMillis, afterID := int64(-1<<62), ""
	for {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		due, err := w.st.ExpiredShareLinks(ctx, cutoff, afterMillis, afterID, linkBatch)
		if err != nil {
			if ctx.Err() != nil {
				return removed, ctx.Err()
			}
			w.log.Error("cannot list ended share links", "error", err)
			return removed, nil
		}
		if len(due) == 0 {
			return removed, nil
		}
		for _, l := range due {
			afterMillis, afterID = l.ExpiresAt.UnixMilli(), l.ID
			files, gone, err := w.removeEndedLink(ctx, cutoff, now, l.AccountID, l.ID)
			if err != nil {
				if ctx.Err() != nil {
					return removed, ctx.Err()
				}
				w.log.Error("cannot remove an ended share link; it is tried again on the next pass",
					"link", l.ID, "account", l.AccountID, "error", err)
				continue
			}
			if !gone {
				continue
			}
			// The row and its uploads' rows are gone in a committed transaction, so no commit can still turn a
			// blob a completion moved into a node. Their files go now; RemoveOrphanUploads is the backstop.
			for _, up := range files {
				if err := w.blobs.RemoveUpload(up.AccountID, up.ID); err != nil {
					w.log.Error("cannot remove a drop upload's file", "upload", up.ID, "error", err)
				}
				if err := w.blobs.Remove(up.AccountID, up.BlobID); err != nil {
					w.log.Error("cannot remove an uncommitted blob", "blob", up.BlobID, "error", err)
				}
			}
			removed++
		}
	}
}

// removeEndedLink removes one ended link in a transaction. It reports whether the link was removed, and the open uploads
// it took with it, whose files the caller removes after the commit.
func (w *Sweeper) removeEndedLink(ctx context.Context, cutoff, now time.Time, accountID, linkID string) ([]*store.Upload, bool, error) {
	var (
		gone  bool
		files []*store.Upload
	)
	err := w.st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.ShareLink(ctx, accountID, linkID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// Checked again inside the write: a change made since the pass's read keeps the link.
		if cur.ExpiresAt == nil || cur.ExpiresAt.UnixMilli() > cutoff.UnixMilli() {
			return nil
		}
		n, err := tx.Node(ctx, cur.AccountID, cur.NodeID)
		if err != nil {
			return err
		}
		ups, err := tx.UploadsCreatedBy(ctx, cur.AccountID, cur.ID, string(auth.KindShareLink))
		if err != nil {
			return err
		}
		for _, up := range ups {
			if err := tx.DeleteUpload(ctx, up.AccountID, up.ID); err != nil {
				return err
			}
			err := tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: up.AccountID,
				ActorKind: "system", ActorID: "sweep", Action: "upload.delete", Target: up.ID,
				Folders: []string{up.FolderID},
				Details: map[string]any{"reason": "link_expired", "length": up.Length,
					"durable_offset": up.DurableOffset}})
			if err != nil {
				return err
			}
		}
		if err := tx.DeleteShareLink(ctx, cur.AccountID, cur.ID); err != nil {
			return err
		}
		err = tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: cur.AccountID,
			ActorKind: "system", ActorID: "sweep", Action: "link.expired", Target: cur.ID,
			Folders: []string{n.FolderID},
			Details: map[string]any{"audience": cur.Audience, "upload_only": cur.UploadOnly,
				"expires_at": cur.ExpiresAt.UTC().Format(time.RFC3339), "uploads": len(ups)}})
		if err != nil {
			return err
		}
		gone, files = true, ups
		return nil
	})
	return files, gone, err
}
