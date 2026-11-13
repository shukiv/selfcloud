// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
)

// fu2: a signed link made for a thumbnail on the content origin, while an installation admin is signed in as
// the user, lives no longer than the sign-in-as (decision 41). The link's own lifetime, here the default 5 minutes,
// is longer than the 3 minutes the impersonation has left, so an uncapped link would outlive it.
func TestThumbnailLinkLivesNoLongerThanTheImpersonation(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true, func(c *config.Config) { c.ContentURL = "https://files.example.test" })
	runThumbnails(t, fx.s)
	created := time.Now().Add(-57 * time.Minute)
	sess, _ := fx.backdatedImpersonation(t, created, time.Now())
	me := decode[meResponse](t, callSession(t, fx.s, sess, "GET", "/api/v1/me", nil))
	if me.PersonalFolderID == nil {
		t.Fatal("the member's My files is not reached while impersonating")
	}
	pic := rawSession(t, fx.s, sess, "POST", "/api/v1/folders/"+*me.PersonalFolderID+"/files?"+q("/pic.png"),
		string(pngBytes(t, 64, 48)), map[string]string{"Content-Type": "image/png"})
	wantStatus(t, pic, http.StatusCreated)
	node := decode[nodeJSON](t, pic)

	var loc string
	deadline := time.Now().Add(60 * time.Second)
	for loc == "" {
		rec := thumbAs(t, fx.s, "", &sess, node.ID, 0)
		switch rec.Code {
		case http.StatusSeeOther:
			loc = rec.Header().Get("Location")
		case http.StatusAccepted:
			if time.Now().After(deadline) {
				t.Fatal("the thumbnail never became ready")
			}
			time.Sleep(20 * time.Millisecond)
		default:
			t.Fatalf("while impersonating: %d %s", rec.Code, rec.Body)
		}
	}
	tok := loc[strings.LastIndex(loc, "/")+1:]
	c, _, _, err := parseLinkToken(tok)
	if err != nil {
		t.Fatalf("Location %q carries no readable link: %v", loc, err)
	}
	if limit := created.Add(impersonationTTL); time.Unix(c.exp, 0).After(limit.Add(time.Second)) {
		t.Errorf("the link lives until %v, past the impersonation's end %v", time.Unix(c.exp, 0), limit)
	}
}
