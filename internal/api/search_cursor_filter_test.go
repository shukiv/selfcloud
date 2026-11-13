// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestSearchCursorRefusesAnotherTypeGroup: a search cursor made under type=pdf does not continue type=image (400
// invalid_request), and it still continues type=pdf (decision 110).
func TestSearchCursorRefusesAnotherTypeGroup(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	for i := range 3 {
		putNode(t, s, key, f.ID, "/cur-"+strconv.Itoa(i)+".pdf", "aa")
	}
	first := pageOf(t, call(t, s, "GET", "/api/v1/search?q=cur-&type=pdf&limit=1", key, nil))
	if first.NextCursor == "" {
		t.Fatal("the first page of type=pdf has no next cursor")
	}
	cursor := url.QueryEscape(first.NextCursor)
	wantError(t, call(t, s, "GET", "/api/v1/search?q=cur-&type=image&limit=1&cursor="+cursor, key, nil),
		400, apierr.InvalidRequest)
	wantStatus(t, call(t, s, "GET", "/api/v1/search?q=cur-&type=pdf&limit=1&cursor="+cursor, key, nil), 200)
}
