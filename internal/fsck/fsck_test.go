// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// fixture is a data directory with a database that has one account, one folder and its root.
// Tests change files and rows directly, the way a server or a disk would.
type fixture struct {
	t      *testing.T
	ctx    context.Context
	dir    string // the data directory
	dbPath string
	st     *store.Store
	blobs  *blob.Dir
	cache  *derived.Cache
	acc    string
	folder string
	root   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := derived.NewFiles(filepath.Join(dir, "derived"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, dir: dir, dbPath: dbPath, st: st, blobs: blobs,
		cache: derived.NewCache(st, files, 1<<30, nil), acc: id.New("acc"), folder: id.New("fld")}
	now := store.Now()
	acc := &store.Account{ID: f.acc, Name: "A", Status: store.AccountActive, CreatedAt: now, UpdatedAt: now}
	fo := &store.Folder{ID: f.folder, AccountID: f.acc, Name: "Docs", NameKey: "docs", CreatedAt: now, UpdatedAt: now}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		return tx.InsertFolder(ctx, fo, "usr_test")
	}); err != nil {
		t.Fatal(err)
	}
	root, err := st.RootNode(ctx, f.acc, f.folder)
	if err != nil {
		t.Fatal(err)
	}
	f.root = root.ID
	return f
}

// commit writes contents as a blob of the account, with no row, and returns its ID and SHA-256.
func (f *fixture) commit(content []byte) (string, string) {
	f.t.Helper()
	w, err := f.blobs.Create(f.ctx, f.acc, 1<<20)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		f.t.Fatal(err)
	}
	c, err := w.Commit()
	if err != nil {
		f.t.Fatal(err)
	}
	return c.ID, c.SHA256
}

// path is where the server keeps a blob: blobs/<account>/<b1>/<b2>/<blob>.
func (f *fixture) path(blobID string) string {
	return filepath.Join(f.dir, "blobs", f.acc, blobID[4:6], blobID[6:8], blobID)
}

// row records a live blob of the given size and hash.
func (f *fixture) row(blobID string, size int64, sha string) {
	f.t.Helper()
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertBlob(f.ctx, &store.Blob{ID: blobID, AccountID: f.acc, Size: size, SHA256: sha,
			State: store.BlobLive, CreatedAt: time.Now()})
	}); err != nil {
		f.t.Fatal(err)
	}
}

// node adds a file named by blobID and returns its ID.
func (f *fixture) node(name, blobID string, size int64, sha string) string {
	f.t.Helper()
	now := time.Now()
	n := &store.Node{ID: id.New("nod"), AccountID: f.acc, FolderID: f.folder, ParentID: f.root, Name: name,
		NameKey: name, Kind: store.KindFile, Size: size, BlobID: blobID, SHA256: sha, CreatedAt: now, ModifiedAt: now,
		CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertNode(f.ctx, n) }); err != nil {
		f.t.Fatal(err)
	}
	return n.ID
}

// version records an earlier content of nodeID named by blobID and returns its ID.
func (f *fixture) version(nodeID, blobID string, size int64, sha string) string {
	f.t.Helper()
	v := &store.Version{ID: id.New("ver"), AccountID: f.acc, NodeID: nodeID, BlobID: blobID, Size: size,
		SHA256: sha, ETag: "e", MimeType: "text/plain", CreatedAt: time.Now(), CreatedBy: "usr_test",
		ReplacedAt: time.Now(), ReplacedBy: "usr_test"}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertVersion(f.ctx, v) }); err != nil {
		f.t.Fatal(err)
	}
	return v.ID
}

// age sets a file's modification time to the given time before now.
func (f *fixture) age(path string, d time.Duration) {
	f.t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		f.t.Fatal(err)
	}
}

// aside moves a file out of the data directory's way, as if it had been lost.
func (f *fixture) aside(path string) {
	f.t.Helper()
	dst := filepath.Join(f.dir, "aside", filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(path, dst); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) run(opts Options) *Report {
	f.t.Helper()
	if opts.DataDir == "" {
		opts.DataDir = f.dir
	}
	if opts.DBPath == "" {
		opts.DBPath = f.dbPath
	}
	if opts.MinAge == 0 {
		opts.MinAge = time.Hour
	}
	r, err := Run(f.ctx, opts)
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return r
}

// find returns the findings of one kind.
func find(r *Report, kind string) []Finding {
	var out []Finding
	for _, x := range r.Findings {
		if x.Kind == kind {
			out = append(out, x)
		}
	}
	return out
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestCleanStoreHasNoFindings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("hello")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	f.node("a.txt", bid, int64(len(content)), sha)
	f.age(f.path(bid), 2*time.Hour)
	r := f.run(Options{VerifyContent: true})
	if len(r.Findings) != 0 {
		t.Fatalf("clean store reported %d findings: %+v", len(r.Findings), r.Findings)
	}
	if r.Integrity != "ok" {
		t.Fatalf("integrity = %q, want ok", r.Integrity)
	}
}

func TestOrphanOlderThanMinAgeIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid, _ := f.commit([]byte("nobody names me"))
	f.age(f.path(bid), 2*time.Hour)
	r := f.run(Options{})
	got := find(r, KindOrphanBlob)
	if len(got) != 1 {
		t.Fatalf("orphan findings = %d, want 1: %+v", len(got), r.Findings)
	}
	if got[0].ID != bid || got[0].Account != f.acc {
		t.Fatalf("orphan names account %q blob %q, want %q %q", got[0].Account, got[0].ID, f.acc, bid)
	}
}

func TestOrphanNewerThanMinAgeIsNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid, _ := f.commit([]byte("just committed"))
	_ = bid
	r := f.run(Options{MinAge: time.Hour})
	if got := find(r, KindOrphanBlob); len(got) != 0 {
		t.Fatalf("a young orphan was reported: %+v", got)
	}
}

func TestMissingNodeBlobIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("gone soon")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	nodeID := f.node("gone.txt", bid, int64(len(content)), sha)
	f.aside(f.path(bid))
	r := f.run(Options{})
	got := find(r, KindMissingBlob)
	if len(got) != 1 {
		t.Fatalf("missing findings = %d, want 1: %+v", len(got), r.Findings)
	}
	if got[0].Node != nodeID || got[0].ID != bid {
		t.Fatalf("missing names node %q blob %q, want %q %q", got[0].Node, got[0].ID, nodeID, bid)
	}
}

func TestMissingVersionBlobIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	old := []byte("the earlier content")
	oldID, oldSHA := f.commit(old)
	f.row(oldID, int64(len(old)), oldSHA)
	cur := []byte("the current content")
	curID, curSHA := f.commit(cur)
	f.row(curID, int64(len(cur)), curSHA)
	nodeID := f.node("doc.txt", curID, int64(len(cur)), curSHA)
	verID := f.version(nodeID, oldID, int64(len(old)), oldSHA)
	f.aside(f.path(oldID))
	r := f.run(Options{})
	got := find(r, KindMissingBlob)
	if len(got) != 1 {
		t.Fatalf("missing findings = %d, want 1: %+v", len(got), r.Findings)
	}
	if got[0].Version != verID || got[0].ID != oldID {
		t.Fatalf("missing names version %q blob %q, want %q %q", got[0].Version, got[0].ID, verID, oldID)
	}
}

func TestSizeMismatchIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("twelve bytes")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content))+5, sha) // the row says a different size than the file has
	f.node("s.txt", bid, int64(len(content))+5, sha)
	r := f.run(Options{})
	got := find(r, KindSizeBlob)
	if len(got) != 1 || got[0].ID != bid {
		t.Fatalf("size findings = %+v, want one for %q", got, bid)
	}
}

func TestHashMismatchFoundOnlyWithVerifyContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("same length, other bytes")
	bid, _ := f.commit(content)
	wrong := sum([]byte("something else entirely!!"))
	f.row(bid, int64(len(content)), wrong)
	f.node("h.txt", bid, int64(len(content)), wrong)
	f.age(f.path(bid), 2*time.Hour)
	if got := find(f.run(Options{}), KindHashBlob); len(got) != 0 {
		t.Fatalf("hash checked without --verify-content: %+v", got)
	}
	got := find(f.run(Options{VerifyContent: true}), KindHashBlob)
	if len(got) != 1 || got[0].ID != bid {
		t.Fatalf("hash findings with --verify-content = %+v, want one for %q", got, bid)
	}
}

func TestSharedBlobIsNotAnOrphan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("copied twice")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	f.node("one.txt", bid, int64(len(content)), sha)
	f.node("two.txt", bid, int64(len(content)), sha)
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{VerifyContent: true}); len(r.Findings) != 0 {
		t.Fatalf("a shared blob was reported: %+v", r.Findings)
	}
}

// Mutant: a version that names a blob must keep it. The blob has no blobs row here, so only the version names it.
func TestVersionOnlyBlobIsNotAnOrphan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("only an old version holds me")
	bid, sha := f.commit(content)
	cur := []byte("the current one")
	curID, curSHA := f.commit(cur)
	f.row(curID, int64(len(cur)), curSHA)
	nodeID := f.node("v.txt", curID, int64(len(cur)), curSHA)
	f.version(nodeID, bid, int64(len(content)), sha)
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{}); len(find(r, KindOrphanBlob)) != 0 {
		t.Fatalf("a version-only blob was reported as an orphan: %+v", r.Findings)
	}
}

// A blob moved into blobs/ by a completed upload, whose upload row still names it, is not an orphan.
func TestUploadRowBlobIsNotAnOrphan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("upload finished, row not yet committed")
	bid, _ := f.commit(content)
	now := time.Now()
	up := &store.Upload{ID: id.New("upl"), AccountID: f.acc, FolderID: f.folder, Name: "u.txt", NameKey: "u.txt",
		OnConflict: "fail", MimeType: "text/plain", Length: int64(len(content)), BlobID: bid, CreatedBy: "usr_test",
		CreatedKind: "user", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error { return tx.InsertUpload(f.ctx, up) }); err != nil {
		t.Fatal(err)
	}
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{}); len(find(r, KindOrphanBlob)) != 0 {
		t.Fatalf("a blob named by an upload row was reported: %+v", r.Findings)
	}
}

func TestRepairMovesTheOrphanAndDeletesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("quarantine me")
	bid, _ := f.commit(content)
	f.age(f.path(bid), 2*time.Hour)
	before := fileSet(t, f.dir)
	r := f.run(Options{Repair: true})
	if len(find(r, KindOrphanBlob)) != 1 {
		t.Fatalf("orphan not found: %+v", r.Findings)
	}
	if r.Moved != 1 || r.Quarantine == "" {
		t.Fatalf("moved = %d, quarantine = %q; want one file moved", r.Moved, r.Quarantine)
	}
	if _, err := os.Stat(f.path(bid)); err == nil {
		t.Fatal("the orphan is still in blobs/")
	}
	q := filepath.Join(f.dir, "quarantine")
	dated, err := filepath.Glob(filepath.Join(q, "*", "blobs", f.acc, bid[4:6], bid[6:8], bid))
	if err != nil || len(dated) != 1 {
		t.Fatalf("quarantined copy: %v %v", dated, err)
	}
	got, err := os.ReadFile(dated[0])
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("quarantined contents differ: %v", err)
	}
	// The manifest sits in the dated folder, next to blobs/.
	manifests, _ := filepath.Glob(filepath.Join(q, "*", "manifest-*.json"))
	if len(manifests) != 1 {
		t.Fatalf("manifest files = %v, want one", manifests)
	}
	// Every file that was there is still there, the orphan under the quarantine folder; the only new file is the manifest.
	after := fileSet(t, f.dir)
	orphan := filepath.Join("blobs", f.acc, bid[4:6], bid[6:8], bid)
	for p := range before {
		if p == orphan {
			continue // moved; its copy is checked above
		}
		if !after[p] {
			t.Fatalf("%s is gone: something was deleted", p)
		}
	}
	var added []string
	for p := range after {
		if !before[p] {
			added = append(added, p)
		}
	}
	if len(added) != 2 { // the quarantined copy and the manifest, and nothing else
		t.Fatalf("new files = %v, want the quarantined copy and the manifest", added)
	}
}

func TestRepairSecondRunIsClean(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid, _ := f.commit([]byte("once"))
	f.age(f.path(bid), 2*time.Hour)
	if r := f.run(Options{Repair: true}); r.Moved != 1 {
		t.Fatalf("first run moved %d, want 1", r.Moved)
	}
	r := f.run(Options{Repair: true})
	if len(r.Findings) != 0 || r.Moved != 0 {
		t.Fatalf("second run: %d findings, %d moved; want clean", len(r.Findings), r.Moved)
	}
}

// Repair never moves a missing blob, a size mismatch or a hash mismatch: there is nothing safe to do with them.
func TestRepairTouchesOnlyOrphans(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("named but lost")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	f.node("lost.txt", bid, int64(len(content)), sha)
	f.aside(f.path(bid))
	r := f.run(Options{Repair: true})
	if len(find(r, KindMissingBlob)) != 1 || r.Moved != 0 {
		t.Fatalf("missing blob: findings %+v, moved %d; want a finding and no move", r.Findings, r.Moved)
	}
}

func TestJSONIsValidAndCountsMatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	orphan, _ := f.commit([]byte("orphan"))
	f.age(f.path(orphan), 2*time.Hour)
	content := []byte("size")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content))+1, sha)
	f.node("s.txt", bid, int64(len(content))+1, sha)
	r := f.run(Options{})
	var buf bytes.Buffer
	if err := r.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Counts   map[string]int `json:"counts"`
		Findings []Finding      `json:"findings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(doc.Findings) != 2 {
		t.Fatalf("JSON findings = %d, want 2", len(doc.Findings))
	}
	perKind := map[string]int{}
	for _, x := range doc.Findings {
		perKind[x.Kind]++
	}
	for kind, n := range perKind {
		if doc.Counts[kind] != n {
			t.Errorf("counts[%q] = %d, findings have %d", kind, doc.Counts[kind], n)
		}
	}
}

// A copy of the database with a damaged page must not pass the integrity check.
func TestIntegrityCheckFailsOnCorruptCopy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i := 0; i < 200; i++ {
		content := []byte(strings.Repeat("x", 300) + string(rune('a'+i%26)) + strings.Repeat("y", i))
		bid, sha := f.commit(content)
		f.row(bid, int64(len(content)), sha)
		f.node("n"+strconv.Itoa(i), bid, int64(len(content)), sha)
	}
	f.st.Close()
	src, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), src...)
	for i := 4096; i < 4096+8192 && i < len(bad); i++ {
		bad[i] ^= 0xA5
	}
	copyPath := filepath.Join(f.dir, "corrupt.db")
	if err := os.WriteFile(copyPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Run(f.ctx, Options{DataDir: f.dir, DBPath: copyPath, MinAge: time.Hour})
	if r == nil {
		t.Fatalf("no report, error %v", err)
	}
	if r.Integrity == "ok" {
		t.Fatalf("integrity = ok for a damaged copy (error %v)", err)
	}
	if len(find(r, KindIntegrity)) == 0 {
		t.Fatalf("no sqlite_integrity finding: %+v", r.Findings)
	}
}

// With the front of the file intact the schema still reads, so integrity_check itself runs and lists its problems. That
// is the path that reports the problems line by line, unlike the unreadable path above.
func TestIntegrityCheckListsProblemsOnDamagedTail(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i := 0; i < 200; i++ {
		content := []byte(strings.Repeat("x", 300) + strconv.Itoa(i))
		bid, sha := f.commit(content)
		f.row(bid, int64(len(content)), sha)
		f.node("n"+strconv.Itoa(i), bid, int64(len(content)), sha)
	}
	f.st.Close()
	src, err := os.ReadFile(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), src...)
	for i := len(bad) - 16384; i < len(bad); i++ {
		bad[i] ^= 0xA5
	}
	copyPath := filepath.Join(f.dir, "damaged-tail.db")
	if err := os.WriteFile(copyPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Run(f.ctx, Options{DataDir: f.dir, DBPath: copyPath, MinAge: time.Hour})
	if r == nil {
		t.Fatalf("no report, error %v", err)
	}
	if r.Integrity != "failed" {
		t.Fatalf("integrity = %q for a damaged copy (error %v), want failed", r.Integrity, err)
	}
	listed := 0
	for _, x := range find(r, KindIntegrity) {
		if !strings.HasPrefix(x.Detail, "the database cannot be read") {
			listed++
		}
	}
	if listed == 0 {
		t.Fatalf("integrity failed but no problem was listed by integrity_check (error %v): %+v", err, r.Findings)
	}
}

// M1: the sweep sets a blob to orphan, removes its file, then deletes the row. A recheck that finds such a row with no
// file must drop the candidate: the sweep got there first.
func TestOrphanStateRowWhoseFileIsGoneIsDroppedOnRecheck(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bid := id.New("blb")
	past := time.Now().Add(-time.Hour)
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertBlob(f.ctx, &store.Blob{ID: bid, AccountID: f.acc, Size: 3, SHA256: sum([]byte("abc")),
			State: store.BlobOrphan, DeleteAfter: &past, CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := store.OpenCheck(f.ctx, f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	c := &checker{ctx: f.ctx, opts: Options{DataDir: f.dir, DBPath: f.dbPath, MinAge: time.Hour,
		DerivedDir: filepath.Join(f.dir, "derived")}, cs: cs,
		rep:   &Report{Counts: map[string]int{}, Scanned: map[string]int{}},
		cands: []candidate{{kind: KindMissingBlob, account: f.acc, id: bid}}}
	if err := c.recheck(); err != nil {
		t.Fatal(err)
	}
	if len(c.rep.Findings) != 0 {
		t.Fatalf("an orphan-state row whose file is gone was reported as missing: %+v", c.rep.Findings)
	}
	if c.rep.Scanned["recheck_dropped"] != 1 {
		t.Fatalf("recheck_dropped = %d, want 1", c.rep.Scanned["recheck_dropped"])
	}
}

// H1: a derived directory other than data_dir/derived is checked where the server keeps its files.
func TestNonDefaultDerivedDirIsChecked(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	custom := filepath.Join(f.dir, "elsewhere")
	content := []byte("thumbnail source")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	f.node("p.jpg", bid, int64(len(content)), sha)
	d := &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: bid, Kind: derived.Kind(256),
		Converter: derived.EncoderVersion + "+test", CreatedAt: time.Now()}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.InsertDerivedPending(f.ctx, d)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out := []byte("thumb bytes")
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.CompleteDerived(f.ctx, f.acc, d.ID, int64(len(out)), "", time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(custom, f.acc, d.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(custom, f.acc, id.New("drv"))
	if err := os.WriteFile(orphan, []byte("no row"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.age(orphan, 2*time.Hour)
	r := f.run(Options{DerivedDir: custom})
	if got := find(r, KindMissingDerived); len(got) != 0 {
		t.Fatalf("a ready row whose file is in the custom directory was reported missing: %+v", got)
	}
	if got := find(r, KindOrphanDerived); len(got) != 1 || got[0].ID != filepath.Base(orphan) {
		t.Fatalf("orphan in the custom directory = %+v, want one", got)
	}
}

func TestMissingReadyDerivedFileIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	content := []byte("thumbnail source")
	bid, sha := f.commit(content)
	f.row(bid, int64(len(content)), sha)
	f.node("p.jpg", bid, int64(len(content)), sha)
	d := &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: bid, Kind: derived.Kind(256),
		Converter: derived.EncoderVersion + "+test", CreatedAt: time.Now()}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.InsertDerivedPending(f.ctx, d)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out := []byte("thumb bytes")
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.CompleteDerived(f.ctx, f.acc, d.ID, int64(len(out)), "", time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p, err := f.cache.Files().Path(f.acc, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := f.run(Options{}); len(find(r, KindMissingDerived)) != 0 || len(r.Findings) != 0 {
		t.Fatalf("a present derived file was reported: %+v", r.Findings)
	}
	f.aside(p)
	got := find(f.run(Options{}), KindMissingDerived)
	if len(got) != 1 || got[0].ID != d.ID {
		t.Fatalf("missing derived findings = %+v, want one for %q", got, d.ID)
	}
}

func TestOrphanDerivedFileIsFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := filepath.Join(f.dir, "derived", f.acc, id.New("drv"))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("no row names me"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.age(p, 2*time.Hour)
	got := find(f.run(Options{}), KindOrphanDerived)
	if len(got) != 1 {
		t.Fatalf("orphan derived findings = %+v, want one", got)
	}
}

// fileSet returns the relative paths of the files under root.
func fileSet(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
