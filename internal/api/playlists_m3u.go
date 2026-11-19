// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// The bounds of the playlist file routes (decision 237).
const (
	// maxPlaylistFileBytes is the largest .m3u or .m3u8 file an import reads.
	maxPlaylistFileBytes = 1 << 20
	// maxImportSkips is how many skipped lines the import answer names; skipped_total holds the whole count.
	maxImportSkips = 100
	// maxImportSegments is the most segments one path line may hold; a longer line is not_found, so the walk is bounded.
	maxImportSegments = 64
	// playlistExportExt is the extension an export gives its file, and playlistExportMime its type.
	playlistExportExt  = ".m3u8"
	playlistExportMime = "audio/x-mpegurl"
)

// playlistImportIn is the body of an import: the drive file to read.
type playlistImportIn struct {
	NodeID string `json:"node_id"`
}

// playlistImportSkip is one line an import skipped, with its 1-based line number and the reason.
type playlistImportSkip struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// playlistImportOut is the import answer: the new playlist with its entries, the skipped lines (at most the first
// maxImportSkips) and their whole count.
type playlistImportOut struct {
	Playlist     playlistDetailJSON   `json:"playlist"`
	Skipped      []playlistImportSkip `json:"skipped"`
	SkippedTotal int                  `json:"skipped_total"`
}

// handleImportPlaylist reads an .m3u or .m3u8 file of the drive and makes a playlist of the files its paths name, in
// file order, resolved from the playlist file's own folder. A line that names no file the caller reaches is skipped
// with its reason, never a refusal of the whole import; the reasons answer alike for every cause they cover, and the
// skips are reported in line order, so the answer does not say which cause it is nor whether a hidden file exists.
// The paths are resolved in one read snapshot, off the writer; the write only re-checks the reach of the kept entries.
// The playlist counts against maxPlaylistsPerUser as a create does, and the create is audited privately to the user,
// with counts and the via only.
func (s *Server) handleImportPlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	var in playlistImportIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	file, role, err := s.nodeAccess(r, in.NodeID)
	if err != nil {
		return err
	}
	if file.Kind != store.KindFile {
		return nodeNotFound()
	}
	lower := strings.ToLower(file.Name)
	if !strings.HasSuffix(lower, ".m3u") && !strings.HasSuffix(lower, ".m3u8") {
		return apierr.New(apierr.InvalidRequest, "the file is not an .m3u or .m3u8 playlist")
	}
	if file.Size > maxPlaylistFileBytes {
		return apierr.Newf(apierr.InvalidRequest, "the playlist file is larger than %d bytes", maxPlaylistFileBytes)
	}
	// An import reads the file's bytes, so the no-download rule binds it as it binds a download.
	restricted, err := s.downloadRestricted(r.Context(), file, role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	text, err := s.readPlaylistFile(file)
	if err != nil {
		return err
	}
	name, err := playlistImportName(file.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	// The paths are resolved in one read snapshot, so the lookups never hold the single writer. The read keeps only
	// the first maxImportSkips skips it will report and counts the rest, so a long file holds no skip per line.
	var kept []importKept
	var skips []playlistImportSkip
	var skippedTotal int
	err = s.store.Read(ctx, func(q store.Querier) error {
		var err error
		kept, skips, skippedTotal, err = resolveImportPaths(ctx, q, p, file.ParentID, text, s.importResolveLookup)
		return err
	})
	if err != nil {
		return err
	}
	at := store.Now()
	pl := store.Playlist{ID: id.New("pls"), AccountID: p.AccountID, UserID: p.UserID, Name: name, CreatedAt: at, UpdatedAt: at}
	entries := make([]store.PlaylistEntry, 0, len(kept))
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := tx.CountPlaylists(ctx, p.UserID)
		if err != nil {
			return err
		}
		if n >= maxPlaylistsPerUser {
			return apierr.Newf(apierr.Conflict, "you may keep at most %d playlists; delete some first", maxPlaylistsPerUser).
				With("reason", "limit").With("limit", maxPlaylistsPerUser)
		}
		// The write re-checks the reach of the kept entries on the state it sees, as addableIn does; one batch serves
		// them all. An entry the caller no longer reaches is skipped, never a refusal of the import.
		hold, more, err := importRecheck(ctx, tx.Querier(), p, kept)
		if err != nil {
			return err
		}
		skips = append(skips, more...)
		skippedTotal += len(more)
		for _, k := range hold {
			entries = append(entries, store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pl.ID, NodeID: k.node, Position: len(entries)})
		}
		if err := tx.InsertPlaylist(ctx, pl, at); err != nil {
			return err
		}
		if len(entries) > 0 {
			if err := tx.ReplacePlaylistEntries(ctx, pl.ID, entries, at); err != nil {
				return err
			}
			pl.EntryCount = len(entries)
		}
		return s.playlistAudit(ctx, tx, r, p, p.AccountID, "playlist.create", pl.ID,
			map[string]any{"via": "import", "count": len(entries), "skipped": skippedTotal})
	})
	if err != nil {
		return err
	}
	// The read's first maxImportSkips skips and the re-check's are merged (at most maxImportSkips +
	// maxPlaylistEntries records), reported in line order and capped at the first maxImportSkips; the total is exact.
	sort.SliceStable(skips, func(i, j int) bool { return skips[i].Line < skips[j].Line })
	if len(skips) > maxImportSkips {
		skips = skips[:maxImportSkips]
	}
	view, err := s.playlistView(ctx, p, pl, entries)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, playlistImportOut{Playlist: view, Skipped: skips, SkippedTotal: skippedTotal})
	return nil
}

// importRecheck re-checks the reach of the kept entries on the state the write sees, in one batch, under the same within
// the read used (a delegated token's node confinement), and returns the entries that still hold and a not_found skip for
// each that does not. An entry the caller no longer reaches, or that moved out of the token's scope, is skipped, never a
// refusal of the import. The route refuses delegated tokens (playlistCaller); this keeps the write's reach in step with
// the read's as defence in depth.
func importRecheck(ctx context.Context, q store.Querier, p *auth.Principal, kept []importKept) ([]importKept, []playlistImportSkip, error) {
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	ids := make([]string, len(kept))
	for i, k := range kept {
		ids[i] = k.node
	}
	roles, err := reachRolesIn(ctx, q, p, ids, within)
	if err != nil {
		return nil, nil, err
	}
	hold := make([]importKept, 0, len(kept))
	var skips []playlistImportSkip
	for _, k := range kept {
		if roles[k.node] == "" {
			skips = append(skips, playlistImportSkip{Line: k.line, Reason: "not_found"})
			continue
		}
		hold = append(hold, k)
	}
	return hold, skips, nil
}

// readPlaylistFile reads a playlist file's bytes through its blob, bounded by the node's size (checked against the cap
// before the read), and decodes them as m3uText does.
func (s *Server) readPlaylistFile(file *store.Node) (string, error) {
	f, err := s.blobs.Open(file.AccountID, file.BlobID)
	if err != nil {
		return "", apierr.Wrap(apierr.Internal, "the file's contents cannot be read", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, file.Size))
	if err != nil {
		return "", apierr.Wrap(apierr.Internal, "the file's contents cannot be read", err)
	}
	return m3uText(data), nil
}

// m3uText decodes a playlist file's bytes: a UTF-8 BOM is dropped, and text that is not valid UTF-8 is read as Latin-1,
// each byte one rune.
func m3uText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for _, c := range data {
		b.WriteRune(rune(c))
	}
	return b.String()
}

// m3uURL reports whether a line is a URL: it has a scheme's ://, file: included.
func m3uURL(line string) bool {
	return strings.Contains(line, "://") || strings.HasPrefix(line, "file:")
}

// m3uAbsolute reports whether a line is an absolute path: a leading / or \, or a drive letter like C:.
func m3uAbsolute(line string) bool {
	if strings.HasPrefix(line, "/") || strings.HasPrefix(line, `\`) {
		return true
	}
	return len(line) >= 2 && line[1] == ':' &&
		((line[0] >= 'a' && line[0] <= 'z') || (line[0] >= 'A' && line[0] <= 'Z'))
}

// importKept is one path line an import keeps: its 1-based line number and the file it names.
type importKept struct {
	line int
	node string
}

// importResolved is one path line resolved as text and structure, before reach is read. climbed holds the ancestors
// the leading .. steps reach, descended the folders the names go down through, and nodeID/nodeKind the target. missing
// is set when a segment does not resolve (or the line has more than maxImportSegments segments); passedRoot when the
// climb goes above the space's root. The reach of climbed, descended and nodeID decides the rest.
type importResolved struct {
	nodeID     string
	nodeKind   string
	climbed    []string
	descended  []string
	passedRoot bool
	missing    bool
}

// resolveImportPaths resolves every path line of the file in one read snapshot, off the writer, and returns the kept
// entries in line order, the first maxImportSkips skipped lines in line order and the whole skip count. name/.. pairs
// fold as text so only leading .. steps climb; every folder a path climbs to or goes down through must be reached, read
// in one batch per path and cached, so a repeated path costs no further query. Once maxPlaylistEntries entries are kept,
// later path lines are limit without a lookup. A line is skipped not_found for a segment that does not resolve, a folder
// or file the caller does not reach, a folder or shortcut target alike; outside only when every ancestor climbed is
// reached and the climb passes the space's root. The reasons answer alike for every cause they cover, so the answer does
// not say whether a hidden file exists. lookup, when set, is called on each Child/Node lookup the resolve makes (tests
// only); reach is read in one batch per path and cached, so it is not counted.
func resolveImportPaths(ctx context.Context, q store.Querier, p *auth.Principal, parentID, text string, lookup func()) ([]importKept, []playlistImportSkip, int, error) {
	hit := func() {
		if lookup != nil {
			lookup()
		}
	}
	hit()
	parent, err := store.NodeIn(ctx, q, p.AccountID, parentID)
	if err != nil {
		return nil, nil, 0, storeErr(err)
	}
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	nodes := map[string]*store.Node{parent.ID: parent}
	children := map[string]*store.Node{}
	paths := map[string]importResolved{}
	reach := map[string]string{}
	node := func(id string) (*store.Node, error) {
		if n, ok := nodes[id]; ok {
			return n, nil
		}
		hit()
		n, err := store.NodeIn(ctx, q, p.AccountID, id)
		if errors.Is(err, store.ErrNotFound) {
			nodes[id] = nil
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		nodes[id] = n
		return n, nil
	}
	child := func(parentID, key string) (*store.Node, error) {
		k := parentID + "\x00" + key
		if n, ok := children[k]; ok {
			return n, nil
		}
		hit()
		n, err := store.ChildIn(ctx, q, p.AccountID, parentID, key)
		if errors.Is(err, store.ErrNotFound) {
			children[k] = nil
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		children[k] = n
		return n, nil
	}
	// resolve folds the line as text and walks it: the leading .. climb the parent chain, the names go down through
	// Child. It reads structure only; reach is decided afterwards, in one batch per path.
	resolve := func(line string) (importResolved, error) {
		var segs []string
		for _, s := range strings.Split(strings.ReplaceAll(line, "\\", "/"), "/") {
			if s != "" && s != "." {
				segs = append(segs, s)
			}
		}
		if len(segs) > maxImportSegments {
			return importResolved{missing: true}, nil
		}
		var stack []string
		for _, s := range segs {
			if s == ".." {
				if len(stack) > 0 && stack[len(stack)-1] != ".." {
					stack = stack[:len(stack)-1]
				} else {
					stack = append(stack, "..")
				}
			} else {
				stack = append(stack, s)
			}
		}
		ups := 0
		for ups < len(stack) && stack[ups] == ".." {
			ups++
		}
		down := stack[ups:]
		cur := parent
		var climbed []string
		if ups > 0 {
			// The climb starts at the playlist file's own folder, which must be reached too: a file-only grant reaches
			// the file but not its folder, so the answer must not say (through outside vs not_found) whether that folder
			// is a space root.
			climbed = append(climbed, parent.ID)
		}
		for i := 0; i < ups; i++ {
			if cur.IsRoot() {
				return importResolved{climbed: climbed, passedRoot: true}, nil
			}
			up, err := node(cur.ParentID)
			if err != nil {
				return importResolved{}, err
			}
			if up == nil {
				return importResolved{missing: true}, nil
			}
			climbed = append(climbed, up.ID)
			cur = up
		}
		var descended []string
		for i, nm := range down {
			_, key, err := names.Clean(nm)
			if err != nil {
				return importResolved{missing: true}, nil
			}
			c, err := child(cur.ID, key)
			if err != nil {
				return importResolved{}, err
			}
			if c == nil {
				return importResolved{missing: true}, nil
			}
			if i == len(down)-1 {
				return importResolved{nodeID: c.ID, nodeKind: c.Kind, climbed: climbed, descended: descended}, nil
			}
			if c.Kind != store.KindDir {
				return importResolved{missing: true}, nil
			}
			descended = append(descended, c.ID)
			cur = c
		}
		// No name follows the climb: the line names cur itself, a folder, which is not a file.
		return importResolved{nodeID: cur.ID, nodeKind: cur.Kind, climbed: climbed, descended: descended}, nil
	}
	// reachFor reads the reach of every node a resolved path names in one batch, caching the result (a node the caller
	// does not reach is cached as the empty role, so a repeated path costs no further query).
	reachFor := func(res importResolved) error {
		ids := make([]string, 0, len(res.climbed)+len(res.descended)+1)
		ids = append(ids, res.climbed...)
		ids = append(ids, res.descended...)
		if res.nodeID != "" {
			ids = append(ids, res.nodeID)
		}
		var ask []string
		for _, id := range ids {
			if _, ok := reach[id]; !ok {
				ask = append(ask, id)
			}
		}
		if len(ask) == 0 {
			return nil
		}
		roles, err := reachRolesIn(ctx, q, p, ask, within)
		if err != nil {
			return err
		}
		for _, id := range ask {
			reach[id] = roles[id]
		}
		return nil
	}
	reason := func(res importResolved) string {
		if res.missing {
			return "not_found"
		}
		for _, id := range res.climbed {
			if reach[id] == "" {
				return "not_found"
			}
		}
		if res.passedRoot {
			return "outside"
		}
		for _, id := range res.descended {
			if reach[id] == "" {
				return "not_found"
			}
		}
		if reach[res.nodeID] == "" || res.nodeKind != store.KindFile {
			return "not_found"
		}
		return ""
	}
	var kept []importKept
	// The read pass meets the lines in order, so it keeps the first maxImportSkips skips it will report and counts
	// the rest; a long file holds no skip record per line. The whole count stays exact.
	skips := make([]playlistImportSkip, 0, maxImportSkips)
	skipped := 0
	skip := func(line int, reason string) {
		skipped++
		if len(skips) < maxImportSkips {
			skips = append(skips, playlistImportSkip{Line: line, Reason: reason})
		}
	}
	seen := make(map[string]bool)
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case m3uURL(line):
			skip(i+1, "url")
			continue
		case m3uAbsolute(line):
			skip(i+1, "absolute")
			continue
		}
		// Once the playlist is full, a later path line is limit without a lookup.
		if len(kept) >= maxPlaylistEntries {
			skip(i+1, "limit")
			continue
		}
		res, ok := paths[line]
		if !ok {
			var err error
			res, err = resolve(line)
			if err != nil {
				return nil, nil, 0, err
			}
			paths[line] = res
		}
		if err := reachFor(res); err != nil {
			return nil, nil, 0, err
		}
		if r := reason(res); r != "" {
			skip(i+1, r)
			continue
		}
		if seen[res.nodeID] {
			skip(i+1, "duplicate")
			continue
		}
		seen[res.nodeID] = true
		kept = append(kept, importKept{line: i + 1, node: res.nodeID})
	}
	return kept, skips, skipped, nil
}

// playlistImportName is the playlist's name from the file's: without its extension, cut to maxPlaylistNameRunes
// characters, through playlistName. A name that leaves nothing takes the whole file name.
func playlistImportName(fileName string) (string, error) {
	cut := len(".m3u")
	if strings.HasSuffix(strings.ToLower(fileName), ".m3u8") {
		cut = len(".m3u8")
	}
	base := fileName[:len(fileName)-cut]
	if runes := []rune(base); len(runes) > maxPlaylistNameRunes {
		base = string(runes[:maxPlaylistNameRunes])
	}
	name, err := playlistName(base)
	if err != nil {
		return playlistName(fileName)
	}
	return name, nil
}

// playlistExportIn is the body of an export: the folder to write the file into.
type playlistExportIn struct {
	FolderID string `json:"folder_id"`
}

// playlistExportOut is the export answer: the new file, as the create routes answer it, and how many entries were left
// out.
type playlistExportOut struct {
	Node    nodeJSON `json:"node"`
	LeftOut int      `json:"left_out"`
}

// handleExportPlaylist writes a playlist into a folder of the drive as an .m3u8 file, named from the playlist with a
// free " (n)" when the name is taken, the way Save As makes a file. Only entries the caller reaches now, that are files
// in the target's space, and whose written path names only folders the caller reaches, go in; the rest are left out and
// counted. The bytes are reserved against the disk and the quota before any lands, and the folder and the name are
// decided again inside the write.
func (s *Server) handleExportPlaylist(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := playlistCaller(p); err != nil {
		return err
	}
	pid := r.PathValue("pls")
	if !id.Valid("pls", pid) {
		return playlistNotFound()
	}
	var in playlistExportIn
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	pl, entries, err := s.playlistOfCaller(ctx, p, pid)
	if err != nil {
		return err
	}
	// The name is cleaned and checked before any byte is written or any write rate charged: a name the rules refuse
	// stops here, spending nothing.
	base := playlistExportBaseName(pl.Name)
	cleanBase, _, err := names.Clean(truncateBytes(base, maxNameBytes-len(playlistExportExt)))
	if err != nil {
		return err
	}
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
	}
	fld, err := requireNodeRole(ctx, s.store, p, in.FolderID, store.RoleContributor)
	if err != nil {
		return err
	}
	if fld.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "folder_id must be a folder")
	}
	content, leftOut, spaceID, parentID, err := s.playlistExportContent(ctx, p, entries, fld)
	if err != nil {
		return err
	}
	c, err := s.writeBlobBytes(ctx, p, p.AccountID, []byte(content))
	if err != nil {
		return err
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		// The folder and the name are decided again on the state this write sees.
		fld, err := requireNodeRole(ctx, tx, p, in.FolderID, store.RoleContributor)
		if err != nil {
			return err
		}
		if fld.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "folder_id must be a folder")
		}
		// The content was built for the folder's place in one read snapshot; if its space or parent changed since,
		// the paths would name the wrong place, so the export is refused rather than written there.
		if fld.FolderID != spaceID || fld.ParentID != parentID {
			return apierr.New(apierr.Conflict, "the folder moved while the export was being written").With("reason", "moved")
		}
		name, key, err := playlistExportName(ctx, tx, p.AccountID, fld.ID, cleanBase)
		if err != nil {
			return err
		}
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: p.AccountID, Size: c.Size,
			SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
			return err
		}
		n, err := s.newNode(ctx, tx, p.AccountID, fld.FolderID, fld.ID, pathSeg{name: name, key: key}, store.KindFile, p.ID)
		if err != nil {
			return err
		}
		n.Size, n.MimeType, n.BlobID, n.ETag, n.SHA256 = c.Size, playlistExportMime, c.ID, newETag(), c.SHA256
		n.ContentTag = store.NewContentTag()
		if err := tx.UpdateNode(ctx, n); err != nil {
			return err
		}
		if err := s.applyUsage(ctx, tx, p, p.AccountID, store.FolderChange{FolderID: fld.FolderID,
			Change: store.UsageChange{Bytes: c.Size, Files: 1}}); err != nil {
			return err
		}
		out = n
		if err := noteEdit(ctx, tx, p, p.AccountID, n.ID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "node.create", n.ID,
			map[string]any{"kind": store.KindFile, "name": n.Name, "parent": fld.ID, "size": c.Size,
				"via": "playlist_export"}, fld.FolderID))
	})
	if err != nil {
		s.discardBlob(p.AccountID, c.ID)
		return err
	}
	writeJSON(w, http.StatusCreated, playlistExportOut{Node: toNodeJSON(out), LeftOut: leftOut})
	return nil
}

// playlistExportContent builds the .m3u8 text in one read snapshot: #EXTM3U, then for each entry in order that the
// caller reaches now, that is a file in the target's space, and whose path names only folders the caller reaches,
// #EXTINF:-1 with the file's name and the path relative to the target folder. Every other entry is left out and
// counted. The reach of the entries and of every folder a path names is read in one batch each for the whole playlist,
// the way placeNames reads reach. It returns the target's space and parent as this snapshot sees them, so the write can
// refuse if the folder moved since.
func (s *Server) playlistExportContent(ctx context.Context, p *auth.Principal, entries []store.PlaylistEntry, target *store.Node) (string, int, string, string, error) {
	var content string
	var leftOut int
	var spaceID, parentID string
	within := ""
	if p.Token != nil {
		within = p.Token.Node
	}
	err := s.store.Read(ctx, func(q store.Querier) error {
		tgt, err := store.NodeIn(ctx, q, p.AccountID, target.ID)
		if err != nil {
			return storeErr(err)
		}
		if tgt == nil {
			return nodeNotFound()
		}
		spaceID, parentID = tgt.FolderID, tgt.ParentID
		ids := make([]string, len(entries))
		for i, e := range entries {
			ids[i] = e.NodeID
		}
		roles, err := reachRolesIn(ctx, q, p, ids, within)
		if err != nil {
			return err
		}
		nodes, err := store.NodesIn(ctx, q, p.AccountID, ids)
		if err != nil {
			return storeErr(err)
		}
		targetPath, err := store.NodePathNamesIn(ctx, q, p.AccountID, tgt.ID)
		if err != nil {
			return storeErr(err)
		}
		type candidate struct {
			node *store.Node
			rel  string   // the path as written
			need int      // how many folders the path names
			cur  string   // the walk's place, from the file's parent up
			down []string // the named folders' IDs, nearest first
			ok   bool     // the walk found every node on the way
		}
		var cands []candidate
		for _, e := range entries {
			n, role := nodes[e.NodeID], roles[e.NodeID]
			if n == nil || role == "" || n.Kind != store.KindFile || n.FolderID != tgt.FolderID {
				leftOut++
				continue
			}
			parentPath, err := store.NodePathNamesIn(ctx, q, p.AccountID, n.ParentID)
			if err != nil {
				return storeErr(err)
			}
			common := 0
			for common < len(targetPath) && common < len(parentPath) && targetPath[common] == parentPath[common] {
				common++
			}
			parts := make([]string, 0, len(targetPath)-common+len(parentPath)-common+1)
			for i := common; i < len(targetPath); i++ {
				parts = append(parts, "..")
			}
			parts = append(parts, parentPath[common:]...)
			parts = append(parts, n.Name)
			rel := strings.Join(parts, "/")
			// A path that would read back as a comment, a url or an absolute path gets ./ in front, so an import
			// resolves it as the relative path it is.
			if strings.HasPrefix(rel, "#") || m3uURL(rel) || m3uAbsolute(rel) {
				rel = "./" + rel
			}
			cands = append(cands, candidate{node: n, rel: rel, need: len(parentPath) - common, cur: n.ParentID, ok: true})
		}
		// The folders a path names are the nearest steps of the walk up from the file's parent, to the common ancestor
		// (the .. steps name nothing). One node read serves every candidate at a level, however long the playlist.
		for level := 0; ; level++ {
			at := make([]string, 0, len(cands))
			for _, c := range cands {
				if c.ok && level < c.need {
					at = append(at, c.cur)
				}
			}
			if len(at) == 0 {
				break
			}
			ups, err := store.NodesIn(ctx, q, p.AccountID, at)
			if err != nil {
				return storeErr(err)
			}
			for i := range cands {
				c := &cands[i]
				if !c.ok || level >= c.need {
					continue
				}
				n := ups[c.cur]
				if n == nil {
					c.ok = false
					continue
				}
				c.down = append(c.down, n.ID)
				c.cur = n.ParentID
			}
		}
		folderIDs := make([]string, 0)
		for _, c := range cands {
			folderIDs = append(folderIDs, c.down...)
		}
		folderRoles, err := reachRolesIn(ctx, q, p, folderIDs, within)
		if err != nil {
			return err
		}
		var b strings.Builder
		b.WriteString("#EXTM3U\n")
		for _, c := range cands {
			reached := c.ok
			for _, id := range c.down {
				if folderRoles[id] == "" {
					reached = false
					break
				}
			}
			if !reached {
				leftOut++
				continue
			}
			b.WriteString("#EXTINF:-1," + c.node.Name + "\n")
			b.WriteString(c.rel + "\n")
		}
		content = b.String()
		return nil
	})
	if err != nil {
		return "", 0, "", "", err
	}
	return content, leftOut, spaceID, parentID, nil
}

// playlistExportBaseName cleans the playlist's name into a file name's base before any byte is written: / and \ become
// -, invisible and format characters are dropped, and a name that leaves nothing becomes Playlist. names.Clean then
// checks the result; a name it still refuses stops the export before any byte or write rate.
func playlistExportBaseName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			b.WriteRune('-')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			// invisible and format characters are dropped
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Playlist"
	}
	return b.String()
}

// playlistExportName is the export file's name: the cleaned base name (playlistExportBaseName) with .m3u8, through
// names.Clean (cut to the name byte cap as Save As cuts), and the first free " (n)" when a sibling holds it, numbered
// from 2 as the owner's wording says, the way saveAsFreeName picks. Decided inside the write, on the state it sees.
func playlistExportName(ctx context.Context, tx *store.Tx, accountID, parentID, name string) (string, string, error) {
	clean, key, err := names.Clean(truncateBytes(name, maxNameBytes-len(playlistExportExt)) + playlistExportExt)
	if err != nil {
		return "", "", err
	}
	taken, err := nameTakenBy(tx.Child(ctx, accountID, parentID, key))
	if err != nil {
		return "", "", err
	}
	if taken == nil {
		return clean, key, nil
	}
	for n := 2; n <= 1000; n++ {
		suffix := fmt.Sprintf(" (%d)%s", n, playlistExportExt)
		cand, key, err := names.Clean(truncateBytes(name, maxNameBytes-len(suffix)) + suffix)
		if err != nil {
			return "", "", err
		}
		taken, err := nameTakenBy(tx.Child(ctx, accountID, parentID, key))
		if err != nil {
			return "", "", err
		}
		if taken == nil {
			return cand, key, nil
		}
	}
	return "", "", apierr.New(apierr.Conflict, "no free name was found for the export")
}
