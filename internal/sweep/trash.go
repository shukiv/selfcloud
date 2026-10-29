// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// purgeTrash purges the explicit trash roots that have been in the trash past trash.retention. Each one
// goes in its own transaction, with its usage and an audit entry under the system actor, so an operator
// can see what the retention removed. The batch is bounded: what is left goes in a later pass.
func (w *Sweeper) purgeTrash(ctx context.Context, now time.Time) (int, error) {
	due, err := w.st.ExpiredTrash(ctx, now.Add(-w.trashKeep), trashBatch)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, n := range due {
		if ctx.Err() != nil {
			return purged, ctx.Err()
		}
		err := w.purgeTrashRoot(ctx, n, now)
		switch {
		case err == nil:
			purged++
		case err == store.ErrNotFound:
			// Gone, restored, or trashed again since the listing: nothing to do.
		default:
			return purged, err
		}
	}
	return purged, nil
}

// purgeTrashRoot purges one listed trash root if it is still due. The check runs inside the transaction,
// against the row as it is then: a restore between the listing and this transaction takes the item out of
// the trash, and a restore followed by a new trashing puts it back in with a fresh trashed_at, which is
// not yet past the retention. Either way the item is not the sweep's to purge, and ErrNotFound says so.
func (w *Sweeper) purgeTrashRoot(ctx context.Context, n *store.Node, now time.Time) error {
	cutoff := now.Add(-w.trashKeep)
	return w.st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.Node(ctx, n.AccountID, n.ID)
		if err != nil {
			return err
		}
		if cur.TrashedAt == nil || cur.TrashedAt.After(cutoff) {
			return store.ErrNotFound
		}
		files, bytes, err := tx.PurgeNode(ctx, cur.AccountID, cur.ID)
		if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: cur.AccountID, ActorKind: "system",
			Action: "node.purge", Target: cur.ID, Folders: []string{cur.FolderID},
			Details: map[string]any{"kind": cur.Kind, "name": cur.Name, "files": files, "bytes": bytes,
				"trashed_by": cur.TrashedBy, "reason": "retention"}})
	})
}

// pruneVersions prunes the versions past versions.max_count or versions.max_age on the nodes that have
// them, a bounded batch at a time. Each node's pruning is one transaction with one audit entry under the
// system actor. Kept versions are never pruned.
func (w *Sweeper) pruneVersions(ctx context.Context, now time.Time) (int, error) {
	var cutoff *time.Time
	if w.versionAge > 0 {
		c := now.Add(-w.versionAge)
		cutoff = &c
	}
	targets, err := w.st.NodesOverVersionLimits(ctx, w.versionCount, cutoff, versionBatch)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, t := range targets {
		if ctx.Err() != nil {
			return pruned, ctx.Err()
		}
		var removed int
		var bytes int64
		err := w.st.Write(ctx, func(tx *store.Tx) error {
			// The entry names the node's folder, so one in a personal folder stays private to its user.
			cur, err := tx.Node(ctx, t.AccountID, t.NodeID)
			if errors.Is(err, store.ErrNotFound) {
				return nil // the node went meanwhile, and its versions went with it
			}
			if err != nil {
				return err
			}
			removed, bytes, err = tx.PruneVersions(ctx, t.AccountID, t.NodeID, w.versionCount, cutoff)
			if err != nil || removed == 0 {
				return err
			}
			return tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: t.AccountID, ActorKind: "system",
				Action: "node.versions_pruned", Target: t.NodeID, Folders: []string{cur.FolderID},
				Details: map[string]any{"versions": removed, "bytes": bytes, "reason": "retention"}})
		})
		if err != nil {
			return pruned, err
		}
		pruned += removed
	}
	return pruned, nil
}
