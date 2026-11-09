// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestGroupOfExtensionDecidesFirst: a known extension decides the group whatever the media type says, so a name and a
// type that disagree are classed by the name.
func TestGroupOfExtensionDecidesFirst(t *testing.T) {
	cases := []struct {
		name, mime, want string
	}{
		{"clip.webm", "application/octet-stream", "video"},
		{"song.ogg", "video/ogg", "audio"},
		{"song.oga", "application/octet-stream", "audio"},
		{"film.ogv", "audio/ogg", "video"},
		{"photo.heic", "application/octet-stream", "image"},
		{"photo.HEIF", "", "image"},
		{"scan.tiff", "text/plain", "image"},
		{"scan.tif", "", "image"},
		{"icon.bmp", "", "image"},
		{"notes.md", "text/plain", "wordprocessing"},
		{"report.pdf", "text/plain", "pdf"},
		{"report.docx", "application/zip", "wordprocessing"},
		{"sheet.xlsx", "image/jpeg", "spreadsheet"},
		{"deck.pptx", "application/octet-stream", "presentation"},
		{"drawing.odg", "application/octet-stream", "drawing"},
		{"bundle.zip", "application/x-zip-compressed", "archive"},
		{"bundle.7z", "", "archive"},
		{"A.DOCX", "", "wordprocessing"},
	}
	for _, c := range cases {
		if got := groupOf("file", c.name, c.mime); got != c.want {
			t.Errorf("groupOf(file, %q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

// TestGroupOfMediaTypeWhenNoKnownExtension: with no known extension, the media type decides: first an exact match
// (parameters dropped, lower case), then the video and audio prefixes.
func TestGroupOfMediaTypeWhenNoKnownExtension(t *testing.T) {
	cases := []struct {
		name, mime, want string
	}{
		{"noext", "application/pdf", "pdf"},
		{"untitled", "video/quicktime", "video"},
		{"untitled", "audio/mpeg; x=1", "audio"},
		{"noext", "application/pdf; name=report", "pdf"},
		{"untitled", "Application/PDF; charset=binary", "pdf"},
		{"untitled", "IMAGE/PNG", "image"},
		{"untitled", "application/x-zip-compressed", "archive"},
		{"untitled", "application/vnd.oasis.opendocument.graphics", "drawing"},
		{"untitled", "text/csv; charset=utf-8", "spreadsheet"},
		{"untitled", "text/markdown", "wordprocessing"},
		{"untitled", "video/x-unknown", "video"},
		{"untitled", "audio/x-unknown", "audio"},
		{"x.bin", "application/octet-stream", ""},
		{"README", "", ""},
		{"untitled", "application/x-unknown", ""},
		{"trailing.", "", ""},
	}
	for _, c := range cases {
		if got := groupOf("file", c.name, c.mime); got != c.want {
			t.Errorf("groupOf(file, %q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

// TestGroupOfFoldersAndShortcutsHaveNoGroup: only a file has a group.
func TestGroupOfFoldersAndShortcutsHaveNoGroup(t *testing.T) {
	for _, kind := range []string{"dir", "shortcut"} {
		if got := groupOf(kind, "report.pdf", "application/pdf"); got != "" {
			t.Errorf("groupOf(%s, report.pdf) = %q, want empty", kind, got)
		}
	}
}

// TestFileGroupsEachMemberOnce: every extension and every media type sits in exactly one group, and the group IDs are the
// ones design section 3 names, in its order.
func TestFileGroupsEachMemberOnce(t *testing.T) {
	seenExt := map[string]string{}
	seenMime := map[string]string{}
	var ids []string
	for _, g := range fileGroups {
		ids = append(ids, g.id)
		for _, e := range g.exts {
			if prev, ok := seenExt[e]; ok {
				t.Errorf("extension %q is in %s and %s", e, prev, g.id)
			}
			seenExt[e] = g.id
		}
		for _, m := range g.mimes {
			if prev, ok := seenMime[m]; ok {
				t.Errorf("media type %q is in %s and %s", m, prev, g.id)
			}
			seenMime[m] = g.id
		}
	}
	want := "wordprocessing spreadsheet presentation drawing pdf image video audio archive"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("group IDs = %q, want %q", got, want)
	}
}

// TestLegacyDocumentKindUnchanged: the legacy kind=document set (storage and API) keeps its old members, so a reader
// of kind=document still gets the same files. The golden is the set as it stood before ff1.
func TestLegacyDocumentKindUnchanged(t *testing.T) {
	const golden = `({base} = 'application/pdf' OR {base} LIKE 'text/%' OR {base} IN ('application/json', 'application/xml',
		'application/javascript', 'application/x-yaml', 'application/yaml', 'application/toml', 'application/x-sh',
		'application/sql', 'application/x-httpd-php', 'application/msword',
		'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'application/vnd.ms-excel',
		'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'application/vnd.ms-powerpoint',
		'application/vnd.openxmlformats-officedocument.presentationml.presentation',
		'application/vnd.oasis.opendocument.text', 'application/vnd.oasis.opendocument.spreadsheet',
		'application/vnd.oasis.opendocument.presentation'))`
	if got := fileKindCond["document"]; got != golden {
		t.Errorf("legacy document set changed:\n got %s\nwant %s", got, golden)
	}
}
