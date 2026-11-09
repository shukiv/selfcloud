// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestSearchTypeFilter: search takes ?type= (a file group, folder or shortcut), which keeps the hits of that type. A type
// and a kind that agree are fine; a kind that disagrees with the type, an unknown type, and a range whose start is not
// before its end are invalid_request.
func TestSearchTypeFilter(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/a.pdf", "aa")
	putNode(t, s, key, f.ID, "/b.docx", "bb")
	mkdirs(t, s, key, f.ID, "/Dir")

	get := func(q string) (int, string) {
		t.Helper()
		rec := call(t, s, "GET", "/api/v1/search?"+q, key, nil)
		return rec.Code, rec.Body.String()
	}
	if code, body := get("type=pdf"); code != 200 || !strings.Contains(body, "a.pdf") || strings.Contains(body, "b.docx") {
		t.Errorf("type=pdf: %d %s; want a.pdf only", code, body)
	}
	if code, body := get("type=folder"); code != 200 || !strings.Contains(body, "Dir") || strings.Contains(body, "a.pdf") {
		t.Errorf("type=folder: %d %s; want Dir only", code, body)
	}
	if code, body := get("type=pdf&kind=file"); code != 200 || !strings.Contains(body, "a.pdf") {
		t.Errorf("type=pdf&kind=file: %d %s; want a.pdf", code, body)
	}
	if code, body := get("type=folder&kind=dir"); code != 200 || !strings.Contains(body, "Dir") {
		t.Errorf("type=folder&kind=dir: %d %s; want Dir", code, body)
	}
	for _, q := range []string{
		"type=pdf&kind=dir",
		"type=folder&kind=file",
		"type=documents",
		"type=pdf&modified_after=5&modified_before=5",
	} {
		if code, body := get(q); code != 400 {
			t.Errorf("%s: %d %s; want 400", q, code, body)
		}
	}
	wantError(t, call(t, s, "GET", "/api/v1/search?type=bogus", key, nil), 400, apierr.InvalidRequest)
}
