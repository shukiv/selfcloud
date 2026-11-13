// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// fileGroup is one type group of the file lists (decision 110). The extensions decide first; the
// media types (lower case, parameters dropped) decide when no extension is known; the prefixes come last.
type fileGroup struct {
	id       string
	exts     []string
	mimes    []string
	prefixes []string
}

// fileGroups is the one table the server reads. The web keeps only the group-to-icon map. Each extension and each media
// type sits in one group (the table test checks it). Changing a member changes the stored type_group of every file with
// that name or type, so a change needs a migration.
var fileGroups = []fileGroup{
	{"wordprocessing", []string{"docx", "doc", "odt", "rtf", "txt", "md"},
		[]string{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/msword",
			"application/vnd.oasis.opendocument.text", "application/rtf", "text/plain", "text/markdown"}, nil},
	{"spreadsheet", []string{"xlsx", "xls", "ods", "csv"},
		[]string{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-excel",
			"application/vnd.oasis.opendocument.spreadsheet", "text/csv"}, nil},
	{"presentation", []string{"pptx", "ppt", "odp"},
		[]string{"application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"application/vnd.ms-powerpoint", "application/vnd.oasis.opendocument.presentation"}, nil},
	{"drawing", []string{"odg"}, []string{"application/vnd.oasis.opendocument.graphics"}, nil},
	{"pdf", []string{"pdf"}, []string{"application/pdf"}, nil},
	{"image", []string{"jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "heic", "heif", "bmp", "tif", "tiff"},
		[]string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "image/svg+xml", "image/heic",
			"image/heif", "image/bmp", "image/tiff"}, nil},
	{"video", []string{"mp4", "mov", "webm", "mkv", "avi", "ogv"}, nil, []string{"video/"}},
	{"audio", []string{"mp3", "m4a", "ogg", "oga", "flac", "wav"}, nil, []string{"audio/"}},
	{"archive", []string{"zip", "tar", "gz", "7z", "rar"},
		[]string{"application/zip", "application/x-zip-compressed", "application/x-tar", "application/gzip",
			"application/x-7z-compressed", "application/x-rar-compressed"}, nil},
}

// groupByExt and groupByMime are the lookups built from fileGroups.
var groupByExt, groupByMime = func() (map[string]string, map[string]string) {
	ext := map[string]string{}
	mime := map[string]string{}
	for _, g := range fileGroups {
		for _, e := range g.exts {
			ext[e] = g.id
		}
		for _, m := range g.mimes {
			mime[m] = g.id
		}
	}
	return ext, mime
}()

// asciiLower folds A to Z only, as SQLite's lower() does without ICU. strings.ToLower folds all of Unicode, and the
// backfill (migration 0053) would then disagree with groupOf on a name such as one with the Kelvin sign.
// Trimming is done with strings.Trim(s, " "), spaces only, as SQLite's trim() does.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// groupOf returns the type group of an entry: "" for a folder, a shortcut, or a file no group knows. A file's extension
// (the text after its last dot, lower case) decides when it is in the table. Otherwise its media type (parameters dropped,
// lower case) decides: first an exact member, then the video and audio prefixes.
func groupOf(kind, name, mime string) string {
	if kind != "file" {
		return ""
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 && i < len(name)-1 {
		if g, ok := groupByExt[asciiLower(name[i+1:])]; ok {
			return g
		}
	}
	base := asciiLower(strings.Trim(strings.SplitN(mime, ";", 2)[0], " "))
	if g, ok := groupByMime[base]; ok {
		return g
	}
	for _, g := range fileGroups {
		for _, p := range g.prefixes {
			if strings.HasPrefix(base, p) {
				return g.id
			}
		}
	}
	return ""
}

// typeGroupFor returns the group node id of accountID gets when it is named name. Its kind and media type are read in
// the caller's transaction, for a raw writer that sets a name without UpdateNodeHeld. A node that is not there gives ""
// and the caller's UPDATE reports it as not found.
func typeGroupFor(ctx context.Context, q querier, accountID, id, name string) (string, error) {
	var kind, mime string
	err := q.QueryRowContext(ctx, `SELECT kind, mime_type FROM nodes WHERE account_id = ? AND id = ?`, accountID, id).Scan(&kind, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return groupOf(kind, name, mime), nil
}
