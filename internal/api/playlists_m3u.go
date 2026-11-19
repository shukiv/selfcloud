// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
// with its reason, never a refusal of the whole import; the reasons answer alike for every cause they cover, so the
// answer does not say which one it is. The playlist counts against maxPlaylistsPerUser as a create does, and the
// create is audited privately to the user, with counts and the via only.
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
	at := store.Now()
	pl := store.Playlist{ID: id.New("pls"), AccountID: p.AccountID, UserID: p.UserID, Name: name, CreatedAt: at, UpdatedAt: at}
	skipped := make([]playlistImportSkip, 0)
	skippedTotal := 0
	entries := make([]store.PlaylistEntry, 0)
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
		skip := func(line int, reason string) {
			skippedTotal++
			if len(skipped) < maxImportSkips {
				skipped = append(skipped, playlistImportSkip{Line: line, Reason: reason})
			}
		}
		parent, err := tx.Node(ctx, p.AccountID, file.ParentID)
		if err != nil {
			return err
		}
		type candidate struct {
			line int
			node string
		}
		var candidates []candidate
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
			nodeID, reason, err := resolveM3UPath(ctx, tx, p.AccountID, parent, line)
			if err != nil {
				return err
			}
			if reason != "" {
				skip(i+1, reason)
				continue
			}
			candidates = append(candidates, candidate{line: i + 1, node: nodeID})
		}
		// The reach of every resolved file is checked as addableIn checks it, on the state this write sees, but per
		// entry: a file the caller does not reach is skipped, never a refusal of the import.
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c.node
		}
		roles, err := reachRolesIn(ctx, tx.Querier(), p, ids, "")
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(candidates))
		for _, c := range candidates {
			if roles[c.node] == "" {
				skip(c.line, "not_found")
				continue
			}
			node, err := tx.Node(ctx, p.AccountID, c.node)
			if errors.Is(err, store.ErrNotFound) {
				skip(c.line, "not_found")
				continue
			}
			if err != nil {
				return err
			}
			if node.Kind != store.KindFile {
				skip(c.line, "not_found")
				continue
			}
			if seen[c.node] {
				skip(c.line, "duplicate")
				continue
			}
			if len(entries) >= maxPlaylistEntries {
				skip(c.line, "limit")
				continue
			}
			seen[c.node] = true
			entries = append(entries, store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pl.ID, NodeID: c.node, Position: len(entries)})
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
	view, err := s.playlistView(ctx, p, pl, entries)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, playlistImportOut{Playlist: view, Skipped: skipped, SkippedTotal: skippedTotal})
	return nil
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

// resolveM3UPath walks one path line from the playlist file's own parent folder: \ becomes /, empty and . segments are
// dropped, .. goes to the parent, and each name goes through names.Clean to its key and Child. It returns the node the
// path names, or the reason the line is skipped: outside when .. would go above the space's root, not_found when a
// segment does not resolve. A name the rules refuse does not resolve either.
func resolveM3UPath(ctx context.Context, tx *store.Tx, accountID string, parent *store.Node, line string) (string, string, error) {
	cur := parent
	for _, seg := range strings.Split(strings.ReplaceAll(line, "\\", "/"), "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			if cur.IsRoot() {
				return "", "outside", nil
			}
			up, err := tx.Node(ctx, accountID, cur.ParentID)
			if err != nil {
				return "", "", err
			}
			cur = up
		default:
			_, key, err := names.Clean(seg)
			if err != nil {
				return "", "not_found", nil
			}
			child, err := tx.Child(ctx, accountID, cur.ID, key)
			if errors.Is(err, store.ErrNotFound) {
				return "", "not_found", nil
			}
			if err != nil {
				return "", "", err
			}
			cur = child
		}
	}
	return cur.ID, "", nil
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
	if err := s.rateLimit(playlistWriteRate, p.UserID); err != nil {
		return err
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
	fld, err := requireNodeRole(ctx, s.store, p, in.FolderID, store.RoleContributor)
	if err != nil {
		return err
	}
	if fld.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "folder_id must be a folder")
	}
	content, leftOut, err := s.playlistExportContent(ctx, p, entries, fld)
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
		name, key, err := playlistExportName(ctx, tx, p.AccountID, fld.ID, pl.Name)
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

// playlistExportContent builds the .m3u8 text: #EXTM3U, then for each entry in order that the caller reaches now, that
// is a file in the target's space, and whose path names only folders the caller reaches, #EXTINF:-1 with the file's
// name and the path relative to the target folder. Every other entry is left out and counted. The reach of the entries
// and of every folder a path names is read in one batch each for the whole playlist, the way placeNames reads reach.
func (s *Server) playlistExportContent(ctx context.Context, p *auth.Principal, entries []store.PlaylistEntry, target *store.Node) (string, int, error) {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.NodeID
	}
	roles, err := reachRoles(ctx, s.store, p, ids)
	if err != nil {
		return "", 0, err
	}
	nodes, err := s.store.NodesByID(ctx, p.AccountID, ids)
	if err != nil {
		return "", 0, err
	}
	targetPath, err := s.store.NodePathNames(ctx, p.AccountID, target.ID)
	if err != nil {
		return "", 0, err
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
	leftOut := 0
	for _, e := range entries {
		n, role := nodes[e.NodeID], roles[e.NodeID]
		if n == nil || role == "" || n.Kind != store.KindFile || n.FolderID != target.FolderID {
			leftOut++
			continue
		}
		parentPath, err := s.store.NodePathNames(ctx, p.AccountID, n.ParentID)
		if err != nil {
			return "", 0, err
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
		if strings.HasPrefix(rel, "#") {
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
		ups, err := s.store.NodesByID(ctx, p.AccountID, at)
		if err != nil {
			return "", 0, err
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
	folderRoles, err := reachRoles(ctx, s.store, p, folderIDs)
	if err != nil {
		return "", 0, err
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
	return b.String(), leftOut, nil
}

// playlistExportName is the export file's name: the playlist's name with .m3u8, through names.Clean (cut to the name
// byte cap as Save As cuts), and the first free " (n)" when a sibling holds it, numbered from 2 as the owner's wording
// says, the way saveAsFreeName picks. Decided inside the write, on the state it sees.
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
