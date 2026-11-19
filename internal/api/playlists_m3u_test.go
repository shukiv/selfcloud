// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// m3uSkip is one skipped line of an import, as the answer names it.
type m3uSkip struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// m3uImportAnswer is the import answer: the new playlist, the skipped lines (at most the first 100) and their whole count.
type m3uImportAnswer struct {
	Playlist     playlistDetailJSON `json:"playlist"`
	Skipped      []m3uSkip          `json:"skipped"`
	SkippedTotal int                `json:"skipped_total"`
}

// m3uExportAnswer is the export answer: the new file and how many entries were left out.
type m3uExportAnswer struct {
	Node    nodeJSON `json:"node"`
	LeftOut int      `json:"left_out"`
}

// plImportCall imports a playlist from a drive file as the session's user.
func plImportCall(t *testing.T, fx *sharingFixture, sess testSession, node string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "POST", "/api/v1/playlists/import", map[string]any{"node_id": node})
}

// plExportCall exports a playlist into a folder as the session's user.
func plExportCall(t *testing.T, fx *sharingFixture, sess testSession, pls, folder string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "POST", "/api/v1/playlists/"+pls+"/export", map[string]any{"folder_id": folder})
}

// m3uContent reads a file's bytes as the session's user.
func m3uContent(t *testing.T, fx *sharingFixture, sess testSession, node string) string {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+node+"/content", nil)
	wantStatus(t, rec, 200)
	return rec.Body.String()
}

// blobCount counts the account's committed blobs, so a refusal can be shown to leave none behind.
func blobCount(t *testing.T, s *Server, accID string) int {
	t.Helper()
	var n int
	err := s.store.Read(context.Background(), func(q store.Querier) error {
		return q.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM blobs WHERE account_id = ?`, accID).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// m3uEntryNames returns the names of a read playlist's entries, in order.
func m3uEntryNames(pl playlistDetailJSON) []string {
	out := make([]string, 0, len(pl.Entries))
	for _, e := range pl.Entries {
		if e.Unavailable || e.Node == nil {
			out = append(out, "<unavailable>")
			continue
		}
		out = append(out, e.Node.Name)
	}
	return out
}

// TestPlaylistImportReadsTheFile: an .m3u with a header, an EXTINF, a blank line and a CRLF line imports its paths in
// file order, resolved from the playlist file's own folder, with backslashes and a walk through the parent.
func TestPlaylistImportReadsTheFile(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/sub")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Other")
	// The walk from Music up to the root and down into Other climbs through the root, so ann must reach it.
	fx.setPerm(t, fx.rootOf(t, fx.fld.ID), fx.ann.ID, "viewer")
	song := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/song.mp3", "s")
	a := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/sub/a.mp3", "a")
	b := putNode(t, fx.s, fx.key, fx.fld.ID, "/Other/b.mp3", "b")
	c := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/sub/c.mp3", "c")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u",
		"#EXTM3U\n#EXTINF:123,song\n\nsong.mp3\r\nsub/a.mp3\n../Other/b.mp3\n.\\sub\\c.mp3\n")

	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if got.Playlist.Name != "list" || got.Playlist.EntryCount != 4 {
		t.Fatalf("imported playlist = %+v", got.Playlist)
	}
	want := []string{song.ID, a.ID, b.ID, c.ID}
	if len(got.Playlist.Entries) != len(want) {
		t.Fatalf("entries = %+v", got.Playlist.Entries)
	}
	for i, e := range got.Playlist.Entries {
		if e.NodeID != want[i] || e.Unavailable || e.Position != i {
			t.Errorf("entry %d = %+v, want node %s at position %d", i, e, want[i], i)
		}
	}
	if len(got.Skipped) != 0 || got.SkippedTotal != 0 {
		t.Errorf("skipped = %+v, total %d, want none", got.Skipped, got.SkippedTotal)
	}
	if !strings.Contains(rec.Body.String(), `"skipped":[]`) {
		t.Errorf("the skipped list is not []: %s", rec.Body.String())
	}
	// The import is audited privately to the user, with counts and the via, never a name or a node.
	rows := auditRows(t, fx, got.Playlist.ID)
	if strings.Join(rows.actions, ",") != "playlist.create" || !strings.Contains(rows.details, `"via":"import"`) {
		t.Fatalf("audit = %v %s", rows.actions, rows.details)
	}
	if strings.Contains(rows.details, "list") || strings.Contains(rows.details, song.ID) {
		t.Errorf("audit details name the file or a node: %s", rows.details)
	}
}

// TestPlaylistImportSkipReasons: every reason a line is skipped, with its 1-based line number. A missing file, one the
// caller does not reach, a folder and a shortcut all skip as not_found, one reason for all.
func TestPlaylistImportSkipReasons(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/sub")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Other")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	song := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/song.mp3", "s")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/sub/a.mp3", "a")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/Other/b.mp3", "b") // ann does not reach Other
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+music.ID+"/shortcuts", fx.key,
		map[string]any{"target": song.ID, "name": "link"}), 201)
	content := strings.Join([]string{
		"#EXTM3U",                   // 1: header, skipped silently
		"#EXTINF:1,song",            // 2: comment, skipped silently
		"",                          // 3: blank, skipped silently
		"song.mp3",                  // 4: kept
		"https://example.com/x.mp3", // 5: url
		"file:///x.mp3",             // 6: url
		"file:x.mp3",                // 7: url
		"/x.mp3",                    // 8: absolute
		`\x.mp3`,                    // 9: absolute
		`C:\x.mp3`,                  // 10: absolute
		"../../x.mp3",               // 11: not_found, the climb reaches the root, which ann does not reach
		"gone.mp3",                  // 12: not_found, missing
		"../Other/b.mp3",            // 13: not_found, not reached
		"sub",                       // 14: not_found, a folder
		"link",                      // 15: not_found, a shortcut
		"song.mp3",                  // 16: duplicate
		"",                          // 17: blank
		"sub/a.mp3",                 // 18: kept
	}, "\n")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", content)

	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	want := []m3uSkip{
		{5, "url"}, {6, "url"}, {7, "url"},
		{8, "absolute"}, {9, "absolute"}, {10, "absolute"},
		{11, "not_found"},
		{12, "not_found"}, {13, "not_found"}, {14, "not_found"}, {15, "not_found"},
		{16, "duplicate"},
	}
	if len(got.Skipped) != len(want) || got.SkippedTotal != len(want) {
		t.Fatalf("skipped = %+v, total %d, want %+v", got.Skipped, got.SkippedTotal, want)
	}
	for i, s := range want {
		if got.Skipped[i] != s {
			t.Errorf("skip %d = %+v, want %+v", i, got.Skipped[i], s)
		}
	}
	if names := m3uEntryNames(got.Playlist); strings.Join(names, ",") != "song.mp3,a.mp3" {
		t.Errorf("entries = %v, want song.mp3 then a.mp3", names)
	}
}

// TestPlaylistImportBOMAndCase: a UTF-8 BOM is dropped, CRLF lines read, and the extension is matched without regard
// to case; the playlist takes the file's name without it.
func TestPlaylistImportBOMAndCase(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	song := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/song.mp3", "s")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/LIST.M3U8", "\xef\xbb\xbf#EXTM3U\r\nsong.mp3\r\n")
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if got.Playlist.Name != "LIST" || got.Playlist.EntryCount != 1 || got.Playlist.Entries[0].NodeID != song.ID {
		t.Fatalf("imported = %+v", got.Playlist)
	}
	if got.SkippedTotal != 0 {
		t.Errorf("skipped total %d, want 0", got.SkippedTotal)
	}
}

// TestPlaylistImportLatin1: text that is not valid UTF-8 reads as Latin-1, each byte one rune, so a Latin-1 file name
// resolves to the UTF-8 name in the drive.
func TestPlaylistImportLatin1(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	cafe := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/caf\u00e9.mp3", "c")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", "caf\xe9.mp3\n")
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if got.Playlist.EntryCount != 1 || got.Playlist.Entries[0].NodeID != cafe.ID {
		t.Fatalf("imported = %+v, want caf\u00e9.mp3", got.Playlist)
	}
}

// TestPlaylistImportOutsideFromRoot: a playlist file at the space's root cannot walk above it.
func TestPlaylistImportOutsideFromRoot(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "viewer")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/root.m3u", "../x.mp3\nsong.mp3\n")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/song.mp3", "s")
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if len(got.Skipped) != 1 || got.Skipped[0] != (m3uSkip{1, "outside"}) || got.SkippedTotal != 1 {
		t.Fatalf("skipped = %+v, total %d", got.Skipped, got.SkippedTotal)
	}
	if names := m3uEntryNames(got.Playlist); strings.Join(names, ",") != "song.mp3" {
		t.Errorf("entries = %v", names)
	}
}

// TestPlaylistImportClimbNeedsTheFilesFolder: a person who reaches only the playlist file (a file share) learns nothing
// from a climb. "../x.mp3" is not_found whether the file sits at the space root or in a subfolder, so the answer does not
// say whether the file's folder is a root: the climb includes the file's own folder, which a file-only grant does not reach.
func TestPlaylistImportClimbNeedsTheFilesFolder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
	}{
		{"root", "/list.m3u8"},
		{"subfolder", "/Sub/list.m3u8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newSharingFixture(t)
			putNode(t, fx.s, fx.key, fx.fld.ID, "/x.mp3", "x")
			if tc.path == "/Sub/list.m3u8" {
				mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Sub")
			}
			list := putNode(t, fx.s, fx.key, fx.fld.ID, tc.path, "../x.mp3\n")
			// ann reaches only the playlist file, not the folder it sits in.
			fx.setPerm(t, list.ID, fx.ann.ID, "viewer")
			rec := plImportCall(t, fx, fx.annSess, list.ID)
			wantStatus(t, rec, 201)
			got := decode[m3uImportAnswer](t, rec)
			if got.Playlist.EntryCount != 0 {
				t.Fatalf("entries = %d, want none", got.Playlist.EntryCount)
			}
			if len(got.Skipped) != 1 || got.Skipped[0] != (m3uSkip{1, "not_found"}) || got.SkippedTotal != 1 {
				t.Fatalf("skipped = %+v, total %d, want line 1 not_found", got.Skipped, got.SkippedTotal)
			}
		})
	}
}

// TestPlaylistImportRecheckUnderToken: the write's re-check honours the token's own confinement, as the read does. A
// principal confined to folder A keeps an entry still in A and skips one moved out of A as not_found. The route itself
// refuses delegated tokens (playlistCaller), so this covers the re-check directly, as defence in depth.
func TestPlaylistImportRecheckUnderToken(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	a := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Alpha")
	b := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Beta")
	inA := putNode(t, fx.s, fx.key, fx.fld.ID, "/Alpha/here.mp3", "h")
	moved := putNode(t, fx.s, fx.key, fx.fld.ID, "/Alpha/gone.mp3", "g")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+moved.ID+"/move", fx.key, map[string]any{"parent": b.ID}), 200)
	p := &auth.Principal{Kind: auth.KindUser, ID: fx.ann.ID, AccountID: fx.acc.ID, UserID: fx.ann.ID,
		Token: &auth.TokenScope{Folders: []string{fx.fld.ID}, Node: a.ID, Write: true, Audience: "api"}}
	kept := []importKept{{line: 1, node: inA.ID}, {line: 2, node: moved.ID}}
	var hold []importKept
	var skips []playlistImportSkip
	err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
		var err error
		hold, skips, err = importRecheck(context.Background(), q, p, kept)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hold) != 1 || hold[0] != (importKept{1, inA.ID}) {
		t.Fatalf("hold = %+v, want the entry still in A", hold)
	}
	if len(skips) != 1 || skips[0] != (playlistImportSkip{2, "not_found"}) {
		t.Fatalf("skips = %+v, want line 2 not_found for the entry moved out of A", skips)
	}
}

// TestPlaylistImportEntryLimit: 501 paths keep the first 500 in order and skip the last as limit.
func TestPlaylistImportEntryLimit(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	// The seeding makes 501 one-request writes from one caller, over the 300 a minute of write.one_request.
	fx.s.limits[oneRequestWriteRate] = newLimiter(1 << 20)
	many := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Many")
	fx.setPerm(t, many.ID, fx.ann.ID, "viewer")
	lines := make([]string, 0, maxPlaylistEntries+1)
	ids := make([]string, 0, maxPlaylistEntries+1)
	for i := 0; i <= maxPlaylistEntries; i++ {
		name := "t" + strconv.Itoa(i) + ".mp3"
		f := putNode(t, fx.s, fx.key, fx.fld.ID, "/Many/"+name, "t")
		lines = append(lines, name)
		ids = append(ids, f.ID)
	}
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Many/list.m3u", strings.Join(lines, "\n"))
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if got.Playlist.EntryCount != maxPlaylistEntries || len(got.Playlist.Entries) != maxPlaylistEntries {
		t.Fatalf("entries = %d, want %d", got.Playlist.EntryCount, maxPlaylistEntries)
	}
	for i, e := range got.Playlist.Entries {
		if e.NodeID != ids[i] {
			t.Fatalf("entry %d = %s, want %s", i, e.NodeID, ids[i])
		}
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != (m3uSkip{maxPlaylistEntries + 1, "limit"}) || got.SkippedTotal != 1 {
		t.Errorf("skipped = %+v, total %d, want line %d limit", got.Skipped, got.SkippedTotal, maxPlaylistEntries+1)
	}
}

// TestPlaylistImportSkippedCapped: the answer names at most the first 100 skipped lines; skipped_total is exact.
func TestPlaylistImportSkippedCapped(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	lines := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		lines = append(lines, "gone"+strconv.Itoa(i)+".mp3")
	}
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", strings.Join(lines, "\n"))
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if len(got.Skipped) != 100 || got.SkippedTotal != 150 {
		t.Fatalf("skipped %d lines, total %d, want 100 and 150", len(got.Skipped), got.SkippedTotal)
	}
	for i, s := range got.Skipped {
		if s.Line != i+1 || s.Reason != "not_found" {
			t.Fatalf("skip %d = %+v, want line %d not_found", i, s, i+1)
		}
	}
	if got.Playlist.EntryCount != 0 {
		t.Errorf("entries = %d, want 0", got.Playlist.EntryCount)
	}
}

// TestPlaylistImportFileRules: the file must be a reachable .m3u or .m3u8 of at most 1 MiB that the caller may
// download; a missing, foreign or unreachable file, and a folder, answer the same not_found.
func TestPlaylistImportFileRules(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	txt := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.txt", "song.mp3\n")
	e := wantError(t, plImportCall(t, fx, fx.annSess, txt.ID), 400, apierr.InvalidRequest)
	if e.Message != "the file is not an .m3u or .m3u8 playlist" {
		t.Errorf("message = %q", e.Message)
	}
	// A folder, a missing node, an unreachable one and a foreign one answer alike.
	missing := plImportCall(t, fx, fx.annSess, id.New("nod"))
	folder := plImportCall(t, fx, fx.annSess, music.ID)
	unreachable := plImportCall(t, fx, fx.bobSess, txt.ID)
	otherAcc, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	ofld := newFolder(t, fx.s, otherKey, otherAcc.ID, "Theirs")
	foreign := putNode(t, fx.s, otherKey, ofld.ID, "/list.m3u", "x\n")
	foreignRec := plImportCall(t, fx, fx.annSess, foreign.ID)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"folder": folder, "unreachable": unreachable, "foreign": foreignRec,
	} {
		wantError(t, rec, 404, apierr.NotFound)
		if !sameAnswer(rec.Body.String(), missing.Body.String()) {
			t.Errorf("%s and missing answer differently:\n%s\n%s", name, rec.Body.String(), missing.Body.String())
		}
	}
	// One byte past the cap is refused, naming it; the cap itself reads.
	big := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/big.m3u", strings.Repeat("x", (1<<20)+1))
	e = wantError(t, plImportCall(t, fx, fx.annSess, big.ID), 400, apierr.InvalidRequest)
	if !strings.Contains(e.Message, "1048576") {
		t.Errorf("size refusal = %q, want it to name the 1 MiB limit", e.Message)
	}
	atCap := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/cap.m3u", strings.Repeat("x", 1<<20))
	wantStatus(t, plImportCall(t, fx, fx.annSess, atCap.ID), 201)
}

// TestPlaylistImportNoDownload: a viewer may not import a file whose downloads are turned off; a contributor may.
func TestPlaylistImportNoDownload(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, music.ID, fx.bob.ID, "contributor")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/song.mp3", "s")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", "song.mp3\n")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+list.ID, fx.key, map[string]any{"no_download": true}), 200)
	wantError(t, plImportCall(t, fx, fx.annSess, list.ID), 403, apierr.DownloadRestricted)
	wantStatus(t, plImportCall(t, fx, fx.bobSess, list.ID), 201)
}

// TestPlaylistImportPlaylistLimit: the 101st playlist is refused with conflict and reason limit, as create is.
func TestPlaylistImportPlaylistLimit(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", "")
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < maxPlaylistsPerUser; i++ {
			if err := tx.InsertPlaylist(context.Background(), store.Playlist{ID: id.New("pls"), AccountID: fx.acc.ID,
				UserID: fx.ann.ID, Name: "p"}, store.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e := wantError(t, plImportCall(t, fx, fx.annSess, list.ID), 409, apierr.Conflict)
	if e.Details["reason"] != "limit" {
		t.Errorf("reason %v, want limit", e.Details["reason"])
	}
}

// TestPlaylistImportName: the name is the file's without its extension, cut to 120 characters; a name that leaves
// nothing takes the whole file name.
func TestPlaylistImportName(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	fx.setPerm(t, music.ID, fx.ann.ID, "viewer")
	for file, want := range map[string]string{
		"/Music/My Mix.m3u8":                          "My Mix",
		"/Music/" + strings.Repeat("x", 150) + ".m3u": strings.Repeat("x", 120),
		"/Music/.m3u":                                 ".m3u",
	} {
		list := putNode(t, fx.s, fx.key, fx.fld.ID, file, "")
		rec := plImportCall(t, fx, fx.annSess, list.ID)
		wantStatus(t, rec, 201)
		if got := decode[m3uImportAnswer](t, rec); got.Playlist.Name != want {
			t.Errorf("name of %q = %q, want %q", file, got.Playlist.Name, want)
		}
	}
}

// TestPlaylistImportExportRefuseTokensAndKeys: a delegated token, even one that acts for the user with write, and the
// account key, are refused on both routes, as on every playlist route.
func TestPlaylistImportExportRefuseTokensAndKeys(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID}, "permissions": "write"})
	for _, cred := range []string{tok, fx.key} {
		wantError(t, call(t, fx.s, "POST", "/api/v1/playlists/import", cred, map[string]any{"node_id": fx.deep.ID}),
			403, apierr.Forbidden)
		wantError(t, call(t, fx.s, "POST", "/api/v1/playlists/"+pls+"/export", cred, map[string]any{"folder_id": fx.b.ID}),
			403, apierr.Forbidden)
	}
}

// TestPlaylistImportExportRefuseImpersonation: an admin signed in as a user is refused by the route table.
func TestPlaylistImportExportRefuseImpersonation(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	for _, path := range []string{"/api/v1/playlists/import", "/api/v1/playlists/pls_none/export"} {
		e := wantError(t, callSession(t, fx.s, imp, "POST", path, map[string]any{}), 403, apierr.Forbidden)
		if e.Details["reason"] != reasonImpersonation {
			t.Errorf("%s: reason %v, want %s", path, e.Details["reason"], reasonImpersonation)
		}
	}
}

// TestPlaylistImportExportRefuseLinkVisitor: a visitor of a link that anyone can open is refused by the route table.
func TestPlaylistImportExportRefuseLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.rootOf(t, fx.fld.ID), nil)
	visitor := fx.visitAnyone(t, secret, "")
	for _, path := range []string{"/api/v1/playlists/import", "/api/v1/playlists/pls_none/export"} {
		rec := fx.visitor(t, visitor, "POST", path)
		if e := wantError(t, rec, 403, apierr.Forbidden); e.Details["reason"] != "link_route" {
			t.Errorf("%s: reason %v, want link_route", path, e.Details["reason"])
		}
	}
}

// TestPlaylistExportPaths: the export writes #EXTM3U and, per entry, #EXTINF with the file's name and the path relative
// to the target folder; the file is the playlist's name with .m3u8. Importing the export gives the same entries back.
func TestPlaylistExportPaths(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/x")
	a := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/a.mp3", "a")
	b := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/x/b.mp3", "b")
	c := putNode(t, fx.s, fx.key, fx.fld.ID, "/c.mp3", "c")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, a.ID, 204)
	plAdd(t, fx, fx.annSess, pls, b.ID, 204)
	plAdd(t, fx, fx.annSess, pls, c.ID, 204)

	rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uExportAnswer](t, rec)
	if got.Node.Name != "Mix.m3u8" || got.Node.Kind != "file" || got.Node.MimeType != "audio/x-mpegurl" || got.LeftOut != 0 {
		t.Fatalf("export = %+v", got)
	}
	want := "#EXTM3U\n#EXTINF:-1,a.mp3\na.mp3\n#EXTINF:-1,b.mp3\n../Music/x/b.mp3\n#EXTINF:-1,c.mp3\n../c.mp3\n"
	if content := m3uContent(t, fx, fx.annSess, got.Node.ID); content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
	if got.Node.Size != int64(len(want)) {
		t.Errorf("size = %d, want %d", got.Node.Size, len(want))
	}
	// The export is audited as a node create with its via.
	audit := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	var found bool
	for _, item := range audit.Items {
		if item.Action == "node.create" && item.Target == got.Node.ID && item.Details["via"] == "playlist_export" {
			found = true
		}
	}
	if !found {
		t.Errorf("no node.create audit with via playlist_export for %s", got.Node.ID)
	}
	// The round trip: importing the exported file gives the same entries, in the same order.
	back := plImportCall(t, fx, fx.annSess, got.Node.ID)
	wantStatus(t, back, 201)
	imp := decode[m3uImportAnswer](t, back)
	if imp.Playlist.Name != "Mix" || imp.Playlist.EntryCount != 3 {
		t.Fatalf("round trip = %+v", imp.Playlist)
	}
	for i, wantID := range []string{a.ID, b.ID, c.ID} {
		if imp.Playlist.Entries[i].NodeID != wantID {
			t.Errorf("round trip entry %d = %s, want %s", i, imp.Playlist.Entries[i].NodeID, wantID)
		}
	}
	if imp.SkippedTotal != 0 {
		t.Errorf("round trip skipped %d", imp.SkippedTotal)
	}
	// A target at the space's root needs no .. at all.
	rec = plExportCall(t, fx, fx.annSess, pls, root)
	wantStatus(t, rec, 201)
	atRoot := decode[m3uExportAnswer](t, rec)
	want = "#EXTM3U\n#EXTINF:-1,a.mp3\nLists/a.mp3\n#EXTINF:-1,b.mp3\nMusic/x/b.mp3\n#EXTINF:-1,c.mp3\nc.mp3\n"
	if content := m3uContent(t, fx, fx.annSess, atRoot.Node.ID); content != want {
		t.Fatalf("root content = %q, want %q", content, want)
	}
}

// TestPlaylistExportFreeName: a second export into the same folder takes the first free " (n)" name.
func TestPlaylistExportFreeName(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	first := decode[m3uExportAnswer](t, plExportCall(t, fx, fx.annSess, pls, lists.ID))
	if first.Node.Name != "Mix.m3u8" {
		t.Fatalf("first = %q", first.Node.Name)
	}
	rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	if second := decode[m3uExportAnswer](t, rec); second.Node.Name != "Mix (2).m3u8" {
		t.Fatalf("second = %q, want Mix (2).m3u8", second.Node.Name)
	}
}

// TestPlaylistExportLeftOut: an entry in another space, and one the caller no longer reaches (here trashed), are left
// out and counted; the rest export.
func TestPlaylistExportLeftOut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	a := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/a.mp3", "a")
	e := putNode(t, fx.s, fx.key, fx.fld.ID, "/e.mp3", "e")
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	otherRoot := fx.rootOf(t, other.ID)
	fx.setPerm(t, otherRoot, fx.ann.ID, "contributor")
	d := putNode(t, fx.s, fx.key, other.ID, "/d.mp3", "d")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, a.ID, 204)
	plAdd(t, fx, fx.annSess, pls, d.ID, 204)
	plAdd(t, fx, fx.annSess, pls, e.ID, 204)
	// The other space goes out of reach, and one file goes to the trash.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+otherRoot+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+e.ID, fx.key, nil), 204)

	rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uExportAnswer](t, rec)
	if got.LeftOut != 2 {
		t.Fatalf("left_out = %d, want 2", got.LeftOut)
	}
	want := "#EXTM3U\n#EXTINF:-1,a.mp3\na.mp3\n"
	if content := m3uContent(t, fx, fx.annSess, got.Node.ID); content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

// TestPlaylistExportUnreachableFolderNames: a path names the folders between the common ancestor and the file; the
// caller must reach every one of them, or the entry is left out. A file-only grant reaches the file but not the folder
// it sits in, so its name is never written; sharing the folder too lets the path out.
func TestPlaylistExportUnreachableFolderNames(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/Lists")
	x := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/x")
	b := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/x/b.mp3", "b")
	fx.setPerm(t, lists.ID, fx.bob.ID, "contributor")
	fx.setPerm(t, b.ID, fx.bob.ID, "viewer") // the file only: bob does not reach Music/x
	pls := plCreate(t, fx, fx.bobSess, "Mix")
	plAdd(t, fx, fx.bobSess, pls, b.ID, 204)

	rec := plExportCall(t, fx, fx.bobSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uExportAnswer](t, rec)
	if got.LeftOut != 1 {
		t.Fatalf("left_out = %d, want 1: the path would name a folder bob does not reach", got.LeftOut)
	}
	if content := m3uContent(t, fx, fx.bobSess, got.Node.ID); content != "#EXTM3U\n" {
		t.Fatalf("content = %q, want the header alone", content)
	}
	// The folder shared too, the path goes out: ../x/b.mp3, one .. for Lists below the common Music.
	fx.setPerm(t, x.ID, fx.bob.ID, "viewer")
	rec = plExportCall(t, fx, fx.bobSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got = decode[m3uExportAnswer](t, rec)
	if got.LeftOut != 0 || got.Node.Name != "Mix (2).m3u8" {
		t.Fatalf("second export = %+v", got)
	}
	want := "#EXTM3U\n#EXTINF:-1,b.mp3\n../x/b.mp3\n"
	if content := m3uContent(t, fx, fx.bobSess, got.Node.ID); content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

// TestPlaylistExportHashPath: a path whose first character is # gets ./ in front, so a player does not read it as a
// comment; the EXTINF line keeps the name as it is.
func TestPlaylistExportHashPath(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	hash := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/#song.mp3", "h")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, hash.ID, 204)
	rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uExportAnswer](t, rec)
	want := "#EXTM3U\n#EXTINF:-1,#song.mp3\n./#song.mp3\n"
	if content := m3uContent(t, fx, fx.annSess, got.Node.ID); content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

// TestPlaylistExportViewerFolder: a folder where the caller is only a viewer refuses the export, and no blob is left
// behind.
func TestPlaylistExportViewerFolder(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	fx.setPerm(t, music.ID, fx.ann.ID, "contributor")
	fx.setPerm(t, lists.ID, fx.ann.ID, "viewer")
	a := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/a.mp3", "a")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, a.ID, 204)
	before := blobCount(t, fx.s, fx.acc.ID)
	wantError(t, plExportCall(t, fx, fx.annSess, pls, lists.ID), 403, apierr.Forbidden)
	if after := blobCount(t, fx.s, fx.acc.ID); after != before {
		t.Errorf("blobs after the refusal = %d, want %d", after, before)
	}
	kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+lists.ID+"/children", fx.key, nil))
	if len(kids.Items) != 0 {
		t.Errorf("the refusal left %d children in the folder", len(kids.Items))
	}
}

// TestPlaylistExportQuotaFull: a full quota refuses the export before any byte lands.
func TestPlaylistExportQuotaFull(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newQuotaAccount(t, s, admin, "Acme", 100)
	f := newFolder(t, s, key, acc.ID, "Docs")
	root, err := s.store.RootNode(context.Background(), acc.ID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+root.ID+"/permissions/"+ann.ID, key,
		map[string]any{"role": "contributor", "notify": false}), 200)
	a := putNode(t, s, key, f.ID, "/a.mp3", "aaaa")
	putNode(t, s, key, f.ID, "/fill.bin", strings.Repeat("f", 96)) // the quota is now full
	pls := decode[playlistJSON](t, callSession(t, s, annSess, "POST", "/api/v1/playlists", map[string]any{"name": "Mix"}))
	wantStatus(t, callSession(t, s, annSess, "POST", "/api/v1/playlists/"+pls.ID+"/entries", map[string]any{"node_id": a.ID}), 204)
	before := blobCount(t, s, acc.ID)
	wantError(t, callSession(t, s, annSess, "POST", "/api/v1/playlists/"+pls.ID+"/export", map[string]any{"folder_id": root.ID}),
		507, apierr.QuotaExceeded)
	if after := blobCount(t, s, acc.ID); after != before {
		t.Errorf("blobs after the refusal = %d, want %d", after, before)
	}
	kids := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+root.ID+"/children", key, nil))
	if len(kids.Items) != 2 {
		t.Errorf("the refusal left %d children, want the 2 it started with", len(kids.Items))
	}
}

// TestPlaylistExportNotAFolder: a file or a shortcut as the target is refused with the folder wording; a playlist of
// another user, and one that does not exist, answer not_found.
func TestPlaylistExportNotAFolder(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	fx.setPerm(t, root, fx.bob.ID, "contributor")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	e := wantError(t, plExportCall(t, fx, fx.annSess, pls, fx.deep.ID), 400, apierr.InvalidRequest)
	if e.Message != "folder_id must be a folder" {
		t.Errorf("message = %q", e.Message)
	}
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/shortcuts", fx.key,
		map[string]any{"target": fx.deep.ID, "name": "link"}), 201)
	kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", fx.key, nil))
	var shortcut string
	for _, k := range kids.Items {
		if k.Kind == "shortcut" {
			shortcut = k.ID
		}
	}
	if shortcut == "" {
		t.Fatal("no shortcut made")
	}
	e = wantError(t, plExportCall(t, fx, fx.annSess, pls, shortcut), 400, apierr.InvalidRequest)
	if e.Message != "folder_id must be a folder" {
		t.Errorf("shortcut message = %q", e.Message)
	}
	other := plExportCall(t, fx, fx.bobSess, pls, fx.b.ID)
	missing := plExportCall(t, fx, fx.bobSess, id.New("pls"), fx.b.ID)
	wantError(t, other, 404, apierr.NotFound)
	if !sameAnswer(other.Body.String(), missing.Body.String()) {
		t.Errorf("another user's playlist and a missing one answer differently:\n%s\n%s", other.Body.String(), missing.Body.String())
	}
}

// TestPlaylistImportSkipLineOrder: the skipped lines are reported in line order, so the list does not say whether a
// file the caller cannot reach exists. [Secret.pdf, http://x] gives the same skipped list whether Secret.pdf is there
// or not: line 1 not_found, line 2 url, in that order.
func TestPlaylistImportSkipLineOrder(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", "Secret.pdf\nhttp://x\n")
	fx.setPerm(t, list.ID, fx.ann.ID, "viewer") // the file only: ann reaches neither Music nor Secret.pdf
	wantStatus(t, plImportCall(t, fx, fx.annSess, list.ID), 201)
	missing := decode[m3uImportAnswer](t, plImportCall(t, fx, fx.annSess, list.ID))
	putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/Secret.pdf", "s") // exists now, but ann still does not reach it
	present := decode[m3uImportAnswer](t, plImportCall(t, fx, fx.annSess, list.ID))
	want := []m3uSkip{{1, "not_found"}, {2, "url"}}
	for name, got := range map[string]m3uImportAnswer{"missing": missing, "present": present} {
		if len(got.Skipped) != len(want) || got.SkippedTotal != len(want) {
			t.Fatalf("%s: skipped = %+v, total %d, want %+v", name, got.Skipped, got.SkippedTotal, want)
		}
		for i, s := range want {
			if got.Skipped[i] != s {
				t.Errorf("%s: skip %d = %+v, want %+v", name, i, got.Skipped[i], s)
			}
		}
	}
}

// TestPlaylistImportUnreachableWalk: a path is resolved without walking through nodes the caller does not reach.
// name/.. pairs fold as text, so Hidden/../../.. climbs the same whether Hidden exists or not; and a path that goes
// down through an unreachable folder is not_found even when the file it names is reached. Hidden/../../.. and
// Hidden/x.mp3 answer alike whether Hidden is there or not.
func TestPlaylistImportUnreachableWalk(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/list.m3u", "Hidden/../../..\nHidden/x.mp3\n")
	fx.setPerm(t, list.ID, fx.ann.ID, "viewer") // the file only: Music and any Hidden are out of reach
	missing := decode[m3uImportAnswer](t, plImportCall(t, fx, fx.annSess, list.ID))
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music/Hidden")
	x := putNode(t, fx.s, fx.key, fx.fld.ID, "/Music/Hidden/x.mp3", "x")
	fx.setPerm(t, x.ID, fx.ann.ID, "viewer") // the file only: ann reaches x.mp3 but not the folder Hidden it sits in
	present := decode[m3uImportAnswer](t, plImportCall(t, fx, fx.annSess, list.ID))
	want := []m3uSkip{{1, "not_found"}, {2, "not_found"}}
	for name, got := range map[string]m3uImportAnswer{"missing": missing, "present": present} {
		if got.Playlist.EntryCount != 0 {
			t.Errorf("%s: kept %d entries, want none (a reachable file behind an unreachable folder is not_found)", name, got.Playlist.EntryCount)
		}
		if len(got.Skipped) != len(want) || got.SkippedTotal != len(want) {
			t.Fatalf("%s: skipped = %+v, total %d, want %+v", name, got.Skipped, got.SkippedTotal, want)
		}
		for i, s := range want {
			if got.Skipped[i] != s {
				t.Errorf("%s: skip %d = %+v, want %+v", name, i, got.Skipped[i], s)
			}
		}
	}
}

// TestPlaylistImportBoundedTime: a 1 MiB file of some 500k one-segment lines imports in bounded time. The first 500
// distinct files are kept in order; every later path line is limit without a lookup, so the work does not grow with
// the line count. The duration is measured and logged.
func TestPlaylistImportBoundedTime(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.limits[oneRequestWriteRate] = newLimiter(1 << 20)
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Many")
	fx.setPerm(t, fx.rootOf(t, fx.fld.ID), fx.ann.ID, "viewer")
	var b strings.Builder
	ids := make([]string, 0, maxPlaylistEntries)
	for i := 0; i < maxPlaylistEntries; i++ {
		name := "t" + strconv.Itoa(i) + ".mp3"
		f := putNode(t, fx.s, fx.key, fx.fld.ID, "/Many/"+name, "t")
		ids = append(ids, f.ID)
		b.WriteString(name + "\n")
	}
	for b.Len() < (1<<20)-2 {
		b.WriteString("x\n")
	}
	content := b.String()
	lines := strings.Count(content, "\n")
	list := putNode(t, fx.s, fx.key, fx.fld.ID, "/Many/list.m3u", content)
	start := time.Now()
	rec := plImportCall(t, fx, fx.annSess, list.ID)
	elapsed := time.Since(start)
	wantStatus(t, rec, 201)
	got := decode[m3uImportAnswer](t, rec)
	if got.Playlist.EntryCount != maxPlaylistEntries || len(got.Playlist.Entries) != maxPlaylistEntries {
		t.Fatalf("entries = %d, want %d", got.Playlist.EntryCount, maxPlaylistEntries)
	}
	for i, e := range got.Playlist.Entries {
		if e.NodeID != ids[i] {
			t.Fatalf("entry %d = %s, want %s", i, e.NodeID, ids[i])
		}
	}
	if got.SkippedTotal != lines-maxPlaylistEntries {
		t.Errorf("skipped total %d, want %d (every line past the 500 kept)", got.SkippedTotal, lines-maxPlaylistEntries)
	}
	if len(got.Skipped) == 0 || got.Skipped[0] != (m3uSkip{maxPlaylistEntries + 1, "limit"}) {
		t.Errorf("first skip = %+v, want line %d limit", got.Skipped[0], maxPlaylistEntries+1)
	}
	t.Logf("import of %d bytes / %d lines took %s", len(content), lines, elapsed)
	if elapsed > 30*time.Second {
		t.Errorf("import took %s, want bounded time (later path lines are limit without a lookup)", elapsed)
	}
}

// TestPlaylistExportNameClean: the export cleans the playlist's name before it writes any byte: / and \ become -,
// invisible and format characters are dropped, and a name that cleans to nothing becomes Playlist. A name the rules
// still refuse is refused before any byte is written or any write rate charged.
func TestPlaylistExportNameClean(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	for name, want := range map[string]string{
		"AC/DC":        "AC-DC.m3u8",
		"a\\b":         "a-b.m3u8",
		"Mix\u200b":    "Mix.m3u8",
		"\u200b\u200c": "Playlist.m3u8",
	} {
		pls := plCreate(t, fx, fx.annSess, name)
		rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
		wantStatus(t, rec, 201)
		if got := decode[m3uExportAnswer](t, rec); got.Node.Name != want {
			t.Errorf("name of %q = %q, want %q", name, got.Node.Name, want)
		}
	}
	// A name the rules refuse even after cleaning (a lone .) writes no byte and leaves no blob.
	pls := plCreate(t, fx, fx.annSess, ".")
	before := blobCount(t, fx.s, fx.acc.ID)
	wantError(t, plExportCall(t, fx, fx.annSess, pls, lists.ID), 400, apierr.InvalidName)
	if after := blobCount(t, fx.s, fx.acc.ID); after != before {
		t.Errorf("blobs after the refused name = %d, want %d (no byte written for a name that fails)", after, before)
	}
}

// TestPlaylistExportUrlAbsolutePath: a relative path whose first segment would read back as a url or an absolute path
// (a C: folder, a file: name) gets ./ in front, so importing the export gives the same entries back.
func TestPlaylistExportUrlAbsolutePath(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists/C:")
	csong := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/C:/song.mp3", "c")
	filex := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/file:x.mp3", "f")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, csong.ID, 204)
	plAdd(t, fx, fx.annSess, pls, filex.ID, 204)
	rec := plExportCall(t, fx, fx.annSess, pls, lists.ID)
	wantStatus(t, rec, 201)
	got := decode[m3uExportAnswer](t, rec)
	want := "#EXTM3U\n#EXTINF:-1,song.mp3\n./C:/song.mp3\n#EXTINF:-1,file:x.mp3\n./file:x.mp3\n"
	if content := m3uContent(t, fx, fx.annSess, got.Node.ID); content != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
	back := plImportCall(t, fx, fx.annSess, got.Node.ID)
	wantStatus(t, back, 201)
	imp := decode[m3uImportAnswer](t, back)
	if imp.Playlist.EntryCount != 2 || imp.SkippedTotal != 0 {
		t.Fatalf("round trip = %+v, skipped %d", imp.Playlist, imp.SkippedTotal)
	}
	for i, wantID := range []string{csong.ID, filex.ID} {
		if imp.Playlist.Entries[i].NodeID != wantID {
			t.Errorf("round trip entry %d = %s, want %s", i, imp.Playlist.Entries[i].NodeID, wantID)
		}
	}
}

// TestPlaylistExportMovedTarget: the content is built in one read snapshot; if the target folder's space or parent
// changes before the write, the export is refused with conflict and reason moved, and no blob is left behind.
func TestPlaylistExportMovedTarget(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "contributor")
	lists := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Lists")
	music := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/Music")
	a := putNode(t, fx.s, fx.key, fx.fld.ID, "/Lists/a.mp3", "a")
	pls := plCreate(t, fx, fx.annSess, "Mix")
	plAdd(t, fx, fx.annSess, pls, a.ID, 204)
	before := blobCount(t, fx.s, fx.acc.ID)
	onceBefore(fx.s, func() {
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+lists.ID+"/move", fx.key, map[string]any{"parent": music.ID}), 200)
	})
	t.Cleanup(func() { fx.s.beforeWrite = nil })
	e := wantError(t, plExportCall(t, fx, fx.annSess, pls, lists.ID), 409, apierr.Conflict)
	if e.Details["reason"] != "moved" {
		t.Errorf("reason %v, want moved", e.Details["reason"])
	}
	if after := blobCount(t, fx.s, fx.acc.ID); after != before {
		t.Errorf("blobs after the moved refusal = %d, want %d", after, before)
	}
}
