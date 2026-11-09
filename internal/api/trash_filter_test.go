// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// trashFilterFixture trashes a.pdf, b.docx, c.jpg, e.pdf and a folder, and returns the account's key.
func trashFilterFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	for _, n := range []string{"/a.pdf", "/b.docx", "/c.jpg", "/e.pdf"} {
		gone := putNode(t, s, key, f.ID, n, "xx")
		wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+gone.ID, key, nil), 204)
	}
	dir := mkdirs(t, s, key, f.ID, "/Dir")
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+dir.ID, key, nil), 204)
	return s, key
}

// TestTrashListFilterByType: the trash keeps the roots of the type: a file group keeps the files of the group, and folder
// keeps the trashed folders. A group with no trashed file is an empty list.
func TestTrashListFilterByType(t *testing.T) {
	t.Parallel()
	s, key := trashFilterFixture(t)
	list := func(q string) string {
		t.Helper()
		rec := call(t, s, "GET", "/api/v1/trash?"+q, key, nil)
		wantStatus(t, rec, 200)
		return rec.Body.String()
	}
	if body := list("type=pdf"); !strings.Contains(body, "a.pdf") || !strings.Contains(body, "e.pdf") || strings.Contains(body, "b.docx") {
		t.Errorf("type=pdf: %s; want a.pdf and e.pdf only", body)
	}
	if body := list("type=wordprocessing"); !strings.Contains(body, "b.docx") || strings.Contains(body, "a.pdf") {
		t.Errorf("type=wordprocessing: %s; want b.docx only", body)
	}
	if body := list("type=folder"); !strings.Contains(body, "Dir") || strings.Contains(body, "a.pdf") {
		t.Errorf("type=folder: %s; want Dir only", body)
	}
	if body := list("type=audio"); !strings.Contains(body, `"items":[]`) {
		t.Errorf("type=audio: %s; want no items", body)
	}
}

// TestTrashListFilterRejects: an unknown type and a range whose start is not before its end are invalid_request.
func TestTrashListFilterRejects(t *testing.T) {
	t.Parallel()
	s, key := trashFilterFixture(t)
	for _, q := range []string{"type=documents", "type=pdf&modified_after=9&modified_before=3", "modified_after=x"} {
		wantError(t, call(t, s, "GET", "/api/v1/trash?"+q, key, nil), 400, apierr.InvalidRequest)
	}
}

// TestTrashListFilterCursorTag: a page of the filtered trash continues only under the same filter.
func TestTrashListFilterCursorTag(t *testing.T) {
	t.Parallel()
	s, key := trashFilterFixture(t)
	rec := call(t, s, "GET", "/api/v1/trash?type=pdf&limit=1", key, nil)
	wantStatus(t, rec, 200)
	body := rec.Body.String()
	i := strings.Index(body, `"next_cursor":"`)
	if i < 0 {
		t.Fatalf("no next cursor in %s", body)
	}
	cursor := body[i+len(`"next_cursor":"`):]
	cursor = cursor[:strings.Index(cursor, `"`)]
	wantError(t, call(t, s, "GET", "/api/v1/trash?limit=1&cursor="+url.QueryEscape(cursor), key, nil),
		400, apierr.InvalidRequest)
	wantStatus(t, call(t, s, "GET", "/api/v1/trash?type=pdf&limit=1&cursor="+url.QueryEscape(cursor), key, nil), 200)
}
