// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"sort"
	"strings"
	"testing"
)

// keysOf is the sorted keys of a set, joined by spaces.
func keysOf(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// TestTextListsArePinned: the server's copies of the editor lists hold exactly these entries. A change here must also
// change web/src/lib/files/kind.ts (and names.ts from ce1), in the same commit.
func TestTextListsArePinned(t *testing.T) {
	t.Parallel()
	pins := []struct {
		name string
		got  map[string]bool
		want string
	}{
		{"codeExtensions", codeExtensions,
			"bash c cc cfg cjs conf cpp css csv cxx env go h hpp htm html ini java js json jsonc jsx log markdown md mjs php properties py rb rs sh sql svg toml ts tsv tsx txt xml yaml yml"},
		{"codeNames", codeNames, "dockerfile makefile"},
		{"textTypes", textTypes, "application/javascript application/json application/sql application/toml application/x-httpd-php application/x-sh application/x-yaml application/xml application/yaml"},
		{"officeTypes", officeTypes, "application/msword application/vnd.ms-excel application/vnd.ms-powerpoint application/vnd.oasis.opendocument.presentation application/vnd.oasis.opendocument.spreadsheet application/vnd.oasis.opendocument.text application/vnd.openxmlformats-officedocument.presentationml.presentation application/vnd.openxmlformats-officedocument.spreadsheetml.sheet application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"archiveTypes", archiveTypes, "application/gzip application/x-7z-compressed application/x-rar-compressed application/x-tar application/zip"},
	}
	for _, p := range pins {
		if got := keysOf(p.got); got != p.want {
			t.Errorf("%s = %q\nwant %q", p.name, got, p.want)
		}
	}
}

// TestUTF8RuleTable: which PUTs must send UTF-8 text. The arguments are the name, the stored type, the request's
// Content-Type and the type the content would be stored as (mimeOf of the request).
func TestUTF8RuleTable(t *testing.T) {
	t.Parallel()
	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	cases := []struct {
		name, stored, header, newType string
		want                          bool
	}{
		{"data.json", "application/json", "", "application/json", true},
		{"Makefile", "application/octet-stream", "", "application/octet-stream", true},
		{"Dockerfile", "application/octet-stream", "", "application/octet-stream", true},
		{"dockerfile", "application/octet-stream", "", "application/octet-stream", true},
		{"MAKEFILE", "application/octet-stream", "", "application/octet-stream", true},
		{"README", "application/octet-stream", "", "application/octet-stream", false},
		{"CONFIG.INI", "application/octet-stream", "", "application/octet-stream", true},
		{"config.yaml", "application/octet-stream", "", "application/octet-stream", true},
		{"a.bin", "application/octet-stream", "", "application/octet-stream", false},
		{"a.bin", "application/octet-stream", "text/plain; charset=utf-8", "application/octet-stream", true},
		{"a.bin", "text/x-foo", "", "text/x-foo", true},
		{"a.bin", "application/yaml", "", "application/yaml", true},
		{"clip.ts", "video/mp2t", "", "video/mp2t", false},
		{"clip.ts", "video/mp2t", "text/plain", "text/plain", false},
		{"pic.txt", "image/png", "", "image/png", false},
		{"notes.txt", "text/plain", "image/png", "image/png", true},
		{"data.json", "application/json", "image/png", "image/png", true},
		{"data.json", "image/png", "", "image/png", false},
		{"a.svg", "image/svg+xml", "", "image/svg+xml", false},
		{"letter.json", docx, "", docx, false},
		{"a.txt", "application/zip", "", "application/zip", false},
		{"a.txt", "application/pdf", "", "application/pdf", false},
		{"a.txt", "text/plain", "", "text/plain", true},
	}
	for _, c := range cases {
		if got := utf8Required(c.name, c.stored, c.header, c.newType); got != c.want {
			t.Errorf("utf8Required(%q, %q, %q, %q) = %v, want %v", c.name, c.stored, c.header, c.newType, got, c.want)
		}
	}
}
