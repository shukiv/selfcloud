// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"mime"
	"path"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/search"
)

// The server's copies of the editor's lists (decision 113). The web's copies are TEXT, OFFICE and
// ARCHIVE in web/src/lib/files/kind.ts, and the code list in web/src/lib/code/names.ts, which ce1 adds. The two sides
// must match. Nothing checks that yet: the cross-check (a Vitest test that compares the lists) comes with ce1, when
// names.ts exists. Until then, change both sides in the same commit.

// codeExtensions is the code list's extensions, without the dot and in lower case.
var codeExtensions = map[string]bool{
	"js": true, "mjs": true, "cjs": true, "jsx": true, "ts": true, "tsx": true,
	"json": true, "jsonc": true, "md": true, "markdown": true, "html": true, "htm": true,
	"xml": true, "svg": true, "css": true, "py": true, "go": true, "rb": true, "php": true,
	"java": true, "c": true, "h": true, "cc": true, "cpp": true, "cxx": true, "hpp": true,
	"rs": true, "sh": true, "bash": true, "sql": true, "yml": true, "yaml": true, "toml": true,
	"ini": true, "conf": true, "cfg": true, "env": true, "properties": true,
	"txt": true, "csv": true, "tsv": true, "log": true,
}

// codeNames is the code list's file names that have no extension, in lower case. The match is in any case, as the
// extensions are.
var codeNames = map[string]bool{"dockerfile": true, "makefile": true}

// textTypes is the TEXT set: media types that are text whatever the name says. A text/* type is text as well.
var textTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/javascript": true,
	"application/x-yaml": true, "application/yaml": true, "application/toml": true,
	"application/x-sh": true, "application/sql": true, "application/x-httpd-php": true,
}

// officeTypes and archiveTypes are the binary document and archive media types.
var officeTypes = map[string]bool{
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.ms-powerpoint":                                             true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.oasis.opendocument.text":                                   true,
	"application/vnd.oasis.opendocument.spreadsheet":                            true,
	"application/vnd.oasis.opendocument.presentation":                           true,
}

var archiveTypes = map[string]bool{
	"application/zip": true, "application/x-tar": true, "application/gzip": true,
	"application/x-7z-compressed": true, "application/x-rar-compressed": true,
}

// mediaType is the media type of a header value, without its parameters, in lower case; "" when it is not one.
func mediaType(v string) string {
	mt, _, err := mime.ParseMediaType(v)
	if err != nil {
		return ""
	}
	return mt
}

// codeNamed reports whether a file name is in the code list: its whole name, or its extension (any case).
func codeNamed(name string) bool {
	if codeNames[strings.ToLower(name)] {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	return ext != "" && codeExtensions[ext]
}

// textMedia reports whether a media type is text: text/* or in the TEXT set.
func textMedia(mt string) bool {
	return strings.HasPrefix(mt, "text/") || textTypes[mt]
}

// binaryMedia reports whether a media type is binary: an image, audio, video, PDF, office document or archive.
func binaryMedia(mt string) bool {
	if strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "video/") {
		return true
	}
	return mt == "application/pdf" || officeTypes[mt] || archiveTypes[mt]
}

// utf8Required reports whether a PUT of a file must send UTF-8 text with no NUL byte (decision 113). The file
// is text-named when its name is in the code list, or its stored type is text, or the request's Content-Type is text.
// A file stored as binary is exempt (an MPEG-TS .ts file stored as video/mp2t, say), unless the request names a text
// type: a binary file that a PUT makes text is checked. A binary request type never switches the rule off for a text
// file, and a checked file keeps its stored type unless the request names a text type (the handler's rule). newType is
// the type the request would store, and it is not consulted.
func utf8Required(name, stored, header, newType string) bool {
	st, hd := mediaType(stored), mediaType(header)
	named := codeNamed(name) || textMedia(st) || textMedia(hd)
	exempt := binaryMedia(st) && !textMedia(hd)
	return named && !exempt
}

// requireUTF8 refuses a committed blob unless it is UTF-8 text with no NUL byte. It reads the blob from disk in chunks
// (search.ValidText), so the body is not held in memory a second time.
func (s *Server) requireUTF8(accountID, blobID string) error {
	f, err := s.blobs.Open(accountID, blobID)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read-only
	if err := search.ValidText(f); err != nil {
		if errors.Is(err, search.ErrNotText) {
			return apierr.New(apierr.InvalidRequest, "the file must be UTF-8 text")
		}
		return err
	}
	return nil
}
