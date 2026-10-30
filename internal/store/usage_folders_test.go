// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// The account's counters are the sum of its folders' (slice p1). These tests run every store path that moves
// usage and check the sum after each step, then check that a recount, which rebuilds both, changes nothing.

// assertFoldersSumToAccount fails when the account's counters differ from the sum of its folders'.
func assertFoldersSumToAccount(t *testing.T, s *Store, accountID, step string) {
	t.Helper()
	var acc, sum Usage
	if err := s.r.QueryRowContext(context.Background(), `SELECT used_bytes, used_files, reserved_bytes FROM accounts WHERE id = ?`,
		accountID).Scan(&acc.Bytes, &acc.Files, &acc.Reserved); err != nil {
		t.Fatal(err)
	}
	if err := s.r.QueryRowContext(context.Background(), `SELECT COALESCE(SUM(used_bytes), 0), COALESCE(SUM(used_files), 0),
		COALESCE(SUM(reserved_bytes), 0) FROM folders WHERE account_id = ?`, accountID).Scan(&sum.Bytes, &sum.Files, &sum.Reserved); err != nil {
		t.Fatal(err)
	}
	if acc != sum {
		t.Fatalf("after %s: account = %+v, sum of folders = %+v", step, acc, sum)
	}
}

// folderCounters reads every folder's counters of an account, keyed by folder ID.
func folderCounters(t *testing.T, s *Store, accountID string) map[string]Usage {
	t.Helper()
	rows, err := s.r.QueryContext(context.Background(), `SELECT id, used_bytes, used_files, reserved_bytes FROM folders WHERE account_id = ?`, accountID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]Usage{}
	for rows.Next() {
		var fid string
		var u Usage
		if err := rows.Scan(&fid, &u.Bytes, &u.Files, &u.Reserved); err != nil {
			t.Fatal(err)
		}
		out[fid] = u
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertRecountAgrees rebuilds the counters from the rows and fails when any folder or the account changed.
func assertRecountAgrees(t *testing.T, s *Store, accountID, step string) {
	t.Helper()
	before := folderCounters(t, s, accountID)
	var accBefore Usage
	if err := s.r.QueryRowContext(context.Background(), `SELECT used_bytes, used_files, reserved_bytes FROM accounts WHERE id = ?`,
		accountID).Scan(&accBefore.Bytes, &accBefore.Files, &accBefore.Reserved); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), func(tx *Tx) error {
		b, a, err := tx.RecountAccount(context.Background(), accountID)
		if err != nil {
			return err
		}
		if b != accBefore {
			t.Errorf("after %s: recount's before = %+v, stored = %+v", step, b, accBefore)
		}
		if a != accBefore {
			t.Errorf("after %s: the counters drifted: recount gives %+v, stored %+v", step, a, accBefore)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := folderCounters(t, s, accountID)
	for fid, u := range before {
		if after[fid] != u {
			t.Errorf("after %s: folder %s counters = %+v, recount gives %+v", step, fid, u, after[fid])
		}
	}
}

// newPersonalRow adds a personal folder for userID to accountID.
func newPersonalRow(t *testing.T, s *Store, accountID, userID string) *Folder {
	t.Helper()
	f := &Folder{ID: id.New("fld"), AccountID: accountID, Name: PersonalFolderName, NameKey: PersonalNameKey(userID),
		Kind: KindPersonal, OwnerUserID: userID, CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertFolder(context.Background(), f, userID) }); err != nil {
		t.Fatal(err)
	}
	return f
}

// addFile puts a file of size bytes into dirID (in folderID) and charges it to the folder, as the API does.
func addFile(t *testing.T, s *Store, accountID, folderID, dirID, name string, size int64) *Node {
	t.Helper()
	ctx := context.Background()
	n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: dirID, Name: name, NameKey: name,
		Kind: KindFile, Size: size, CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, n); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, accountID, folderID, UsageChange{Bytes: size, Files: 1})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// addDir puts a directory under parentID.
func addDir(t *testing.T, s *Store, accountID, folderID, parentID, name string) *Node {
	t.Helper()
	n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: folderID, ParentID: parentID, Name: name, NameKey: name,
		Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertNode(context.Background(), n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// addVersion keeps a version of size bytes for node n, and charges it, as a content replacement does.
func addVersion(t *testing.T, s *Store, accountID string, n *Node, size int64) {
	t.Helper()
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertVersion(ctx, &Version{ID: id.New("ver"), AccountID: accountID, NodeID: n.ID, BlobID: id.New("blb"),
			Size: size, CreatedAt: Now(), ReplacedAt: Now()}); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, accountID, n.FolderID, UsageChange{Bytes: size})
	}); err != nil {
		t.Fatal(err)
	}
}

// upload opens an unfinished upload of length bytes whose target is parentID (or the folder's root when parentID is "").
func upload(t *testing.T, s *Store, accountID, folderID, parentID string, length int64) *Upload {
	t.Helper()
	return newUploadRow(t, s, accountID, folderID, parentID, length)
}

// TestCountersSumToAccountAcrossPaths runs uploads, versions, moves across folders, a subtree move into a
// team space, a contents move on user deletion, trash, purge, expiry and folder deletion, checking the sum
// of the folders against the account and a recount after every step.
func TestCountersSumToAccountAcrossPaths(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, acc, "ann@x", "")
	bob := newUserRow(t, s, acc, "bob@x", "")
	mine := newPersonalRow(t, s, acc, ann.ID)
	other := newPersonalRow(t, s, acc, bob.ID)
	team := newFolderRow(t, s, acc, "Team", "team")
	teamRoot, err := s.RootNode(ctx, acc, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	myRoot, err := s.RootNode(ctx, acc, mine.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Files, a version, and an upload into a directory and one into the root.
	f1 := addFile(t, s, acc, mine.ID, myRoot.ID, "a.bin", 100)
	addVersion(t, s, acc, f1, 30)
	dir := addDir(t, s, acc, mine.ID, myRoot.ID, "d")
	addFile(t, s, acc, mine.ID, dir.ID, "b.bin", 50)
	inDir := upload(t, s, acc, mine.ID, dir.ID, 60)
	atRoot := upload(t, s, acc, mine.ID, "", 40)
	assertFoldersSumToAccount(t, s, acc, "uploads and files")
	assertRecountAgrees(t, s, acc, "uploads and files")

	// Move the directory, with its file and the upload aimed into it, into the team space.
	if err := s.Write(ctx, func(tx *Tx) error {
		dir.ParentID, dir.FolderID = teamRoot.ID, team.ID
		if err := tx.UpdateNode(ctx, dir); err != nil {
			return err
		}
		return tx.MoveSubtree(ctx, acc, dir.ID, mine.ID, team.ID)
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "move into a team space")
	assertRecountAgrees(t, s, acc, "move into a team space")
	if u := folderCounters(t, s, acc)[team.ID]; u.Reserved != 60 || u.Bytes != 50 {
		t.Errorf("team space after the move = %+v; want the directory's 50 bytes and the upload's 60 reserved", u)
	}

	// Complete the upload aimed into the moved directory: its reservation becomes a file in the team space.
	if err := s.Write(ctx, func(tx *Tx) error {
		done := &Node{ID: id.New("nod"), AccountID: acc, FolderID: team.ID, ParentID: dir.ID, Name: "up.bin", NameKey: "up.bin",
			Kind: KindFile, Size: 60, CreatedAt: Now(), ModifiedAt: Now()}
		if err := tx.InsertNode(ctx, done); err != nil {
			return err
		}
		if err := tx.DeleteUpload(ctx, acc, inDir.ID); err != nil {
			return err
		}
		return tx.AdjustUsage(ctx, acc, team.ID, UsageChange{Bytes: 60, Files: 1})
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "completion in a moved directory")
	assertRecountAgrees(t, s, acc, "completion in a moved directory")

	// Expire the root upload through the sweep's path.
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.DeleteExpiredUpload(ctx, acc, atRoot.ID, Now().Add(time.Hour), Now().Add(-time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "expiry")
	assertRecountAgrees(t, s, acc, "expiry")

	// Trash the moved directory's subtree, then purge it: its files, versions and the uploads aimed into it go.
	upAgain := upload(t, s, acc, team.ID, dir.ID, 25)
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.DropUploadsUnder(ctx, acc, dir.ID); err != nil {
			return err
		}
		return tx.TrashNode(ctx, acc, dir.ID, ann.ID, Now())
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "trash")
	assertRecountAgrees(t, s, acc, "trash")
	_ = upAgain
	if err := s.Write(ctx, func(tx *Tx) error {
		_, _, err := tx.PurgeNode(ctx, acc, dir.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "purge")
	assertRecountAgrees(t, s, acc, "purge")

	// Contents move on user deletion: the source keeps only its root's uploads, and the destination takes the rest.
	rootUp := upload(t, s, acc, mine.ID, "", 15)
	deepDir := addDir(t, s, acc, mine.ID, myRoot.ID, "e")
	addFile(t, s, acc, mine.ID, deepDir.ID, "c.bin", 70)
	aimed := upload(t, s, acc, mine.ID, deepDir.ID, 9)
	otherRoot, err := s.RootNode(ctx, acc, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.MoveFolderContents(ctx, acc, mine.ID, other.ID, otherRoot.ID, bob.ID)
	}); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "contents move")
	assertRecountAgrees(t, s, acc, "contents move")
	if u := folderCounters(t, s, acc)[other.ID]; u.Reserved != 9 {
		t.Errorf("destination reserved after the contents move = %d; want the 9 bytes aimed into the moved directory", u.Reserved)
	}
	_ = rootUp
	_ = aimed

	// Delete the source personal folder: its root's upload goes with it.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, mine.ID) }); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "source folder deletion")
	assertRecountAgrees(t, s, acc, "source folder deletion")

	// Delete the team space: everything in it goes, uploads included.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, team.ID) }); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "team space deletion")
	assertRecountAgrees(t, s, acc, "team space deletion")
	// The contents moved in were the root's files (a.bin, 100 bytes, with its 30-byte version) and c.bin (70).
	if u := folderCounters(t, s, acc)[other.ID]; u != (Usage{Bytes: 200, Files: 2, Reserved: 9}) {
		t.Errorf("destination after the others went = %+v; want the 200 bytes of the two files and its 9 reserved bytes", u)
	}
}

// TestCounterPathsMoveReservationsWithTheirDirectory checks the rule that a reservation counts in the folder its
// upload names, and that a move of the directory moves the reservation with it. An upload aimed at a directory
// that a move carried away is found in the destination when its folder is deleted.
func TestCounterPathsMoveReservationsWithTheirDirectory(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	g := newFolderRow(t, s, acc, "Other", "other")
	root, _ := s.RootNode(ctx, acc, f.ID)
	groot, _ := s.RootNode(ctx, acc, g.ID)
	dir := addDir(t, s, acc, f.ID, root.ID, "d")
	upload(t, s, acc, f.ID, dir.ID, 400)
	if err := s.Write(ctx, func(tx *Tx) error {
		dir.ParentID, dir.FolderID = groot.ID, g.ID
		if err := tx.UpdateNode(ctx, dir); err != nil {
			return err
		}
		return tx.MoveSubtree(ctx, acc, dir.ID, f.ID, g.ID)
	}); err != nil {
		t.Fatal(err)
	}
	counters := folderCounters(t, s, acc)
	if counters[f.ID].Reserved != 0 || counters[g.ID].Reserved != 400 {
		t.Fatalf("reservations after the move = %+v; want 0 in Docs and 400 in Other", counters)
	}
	assertFoldersSumToAccount(t, s, acc, "move of a reservation")
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteFolder(ctx, acc, g.ID) }); err != nil {
		t.Fatal(err)
	}
	assertFoldersSumToAccount(t, s, acc, "deleting the destination")
	if got := usageRow(t, s, acc).Reserved; got != 0 {
		t.Errorf("account reserved after deleting the destination = %d; want 0", got)
	}
}

// TestBackfillFromMigration0016 builds a database as it was at migration 0016, with files, versions and uploads,
// opens it with this binary, and checks that the new folder counters match the rows. An upload whose directory had
// been moved carries the directory's folder after the upgrade.
func TestBackfillFromMigration0016(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "journal_mode(WAL)"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	old.SetMaxOpenConns(1)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := old.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`)
	for _, m := range migs[:16] {
		exec(m.sql)
		exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 0)", m.version, m.name)
	}
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at, reserved_bytes, used_bytes, used_files)
		VALUES ('acc_a', 'A', 'active', 0, 0, 0, 0, 0)`)
	exec(`INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
		VALUES ('usr_ann', 'acc_a', 'ann@x', 'ann@x', 'member', 'active', 0, 0)`)
	exec(`INSERT INTO folders (id, account_id, name, name_key, kind, owner_user_id, created_at, updated_at) VALUES
		('fld_ann', 'acc_a', 'My files', '/personal/usr_ann', 'personal', 'usr_ann', 0, 0),
		('fld_docs', 'acc_a', 'Docs', 'docs', 'team', NULL, 0, 0)`)
	exec(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, created_at, modified_at, created_by)
		VALUES ('nod_ann', 'acc_a', 'fld_ann', NULL, '', '', 'dir', 0, 0, 0, ''),
		       ('nod_docs', 'acc_a', 'fld_docs', NULL, '', '', 'dir', 0, 0, 0, ''),
		       ('nod_d', 'acc_a', 'fld_docs', 'nod_docs', 'd', 'd', 'dir', 0, 0, 0, ''),
		       ('nod_f', 'acc_a', 'fld_ann', 'nod_ann', 'f', 'f', 'file', 100, 0, 0, ''),
		       ('nod_g', 'acc_a', 'fld_docs', 'nod_d', 'g', 'g', 'file', 50, 0, 0, '')`)
	exec(`INSERT INTO versions (id, account_id, node_id, blob_id, size, sha256, etag, mime_type, created_at, created_by, replaced_at, replaced_by, keep_forever)
		VALUES ('ver_1', 'acc_a', 'nod_f', 'blb_1', 30, '', '', '', 0, '', 0, '', 0)`)
	// An upload aimed at nod_d, which a move left in Docs but whose row still names My files.
	exec(`INSERT INTO uploads (id, account_id, folder_id, parent_id, dir_path, parents, name, name_key, on_conflict, mime_type,
		length, durable_offset, blob_id, created_by, created_kind, created_at, updated_at, expires_at)
		VALUES ('upl_1', 'acc_a', 'fld_ann', 'nod_d', '', 0, 'x', 'x', 'fail', '', 40, 0, 'blb_u1', 'usr_ann', 'user', 0, 0, 9999999999999)`)
	exec(`INSERT INTO uploads (id, account_id, folder_id, parent_id, dir_path, parents, name, name_key, on_conflict, mime_type,
		length, durable_offset, blob_id, created_by, created_kind, created_at, updated_at, expires_at)
		VALUES ('upl_2', 'acc_a', 'fld_ann', NULL, '', 0, 'y', 'y', 'fail', '', 15, 0, 'blb_u2', 'usr_ann', 'user', 0, 0, 9999999999999)`)
	// Deliberately wrong account counters, as a drifted install would have: the migration must make them the sum of
	// the folders' rebuilt counters (180 bytes, 2 files, 55 reserved), so the first sweep has nothing to report.
	exec(`UPDATE accounts SET used_bytes = 999, used_files = 9, reserved_bytes = 1 WHERE id = 'acc_a'`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatalf("open at 0016: %v", err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(ctx); v != len(migs) {
		t.Fatalf("schema version = %d, want %d", v, len(migs))
	}
	counters := folderCounters(t, s, "acc_a")
	// My files: f (100) and its version (30), 1 file, and the root upload (15). Docs: d's g (50) and the upload
	// aimed at d (40) now that d is in Docs.
	if got := counters["fld_ann"]; got != (Usage{Bytes: 130, Files: 1, Reserved: 15}) {
		t.Errorf("My files after backfill = %+v; want {130 1 15}", got)
	}
	if got := counters["fld_docs"]; got != (Usage{Bytes: 50, Files: 1, Reserved: 40}) {
		t.Errorf("Docs after backfill = %+v; want {50 1 40}", got)
	}
	if got := usageRow(t, s, "acc_a"); got != (Usage{Bytes: 180, Files: 2, Reserved: 55}) {
		t.Errorf("account after backfill = %+v; want the sum of its folders {180 2 55}", got)
	}
	assertFoldersSumToAccount(t, s, "acc_a", "backfill")
	assertRecountAgrees(t, s, "acc_a", "backfill")
	var quota sql.NullInt64
	if err := s.r.QueryRowContext(ctx, "SELECT quota_bytes FROM users WHERE id = 'usr_ann'").Scan(&quota); err != nil || quota.Valid {
		t.Errorf("users.quota_bytes after backfill = %+v, %v; want NULL (follows the default)", quota, err)
	}
	if u, err := s.Account(ctx, "acc_a"); err != nil || u.UserQuotaBytes != 0 {
		t.Errorf("user_quota_bytes after backfill = %+v, %v; want 0 (no limit)", u, err)
	}
}

// TestPurgeReleasesReservationsAimedIntoTheSubtree: a pending upload aimed into a directory that is trashed and then
// purged gives its reservation back; the folder and the account return to their values before the upload.
func TestPurgeReleasesReservationsAimedIntoTheSubtree(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	f := newFolderRow(t, s, acc, "Docs", "docs")
	root, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := addDir(t, s, acc, f.ID, root.ID, "d")
	before := folderCounters(t, s, acc)[f.ID]
	accBefore := usageRow(t, s, acc)
	upload(t, s, acc, f.ID, dir.ID, 60)
	if got := folderCounters(t, s, acc)[f.ID].Reserved; got != 60 {
		t.Fatalf("reserved after the upload = %d; want 60", got)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.TrashNode(ctx, acc, dir.ID, "usr_test", Now()) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { _, _, err := tx.PurgeNode(ctx, acc, dir.ID); return err }); err != nil {
		t.Fatal(err)
	}
	if got := folderCounters(t, s, acc)[f.ID]; got != before {
		t.Errorf("folder counters after purge = %+v; want the pre-upload %+v", got, before)
	}
	if got := usageRow(t, s, acc); got != accBefore {
		t.Errorf("account counters after purge = %+v; want the pre-upload %+v", got, accBefore)
	}
	assertFoldersSumToAccount(t, s, acc, "purge of a directory with a pending upload")
}
