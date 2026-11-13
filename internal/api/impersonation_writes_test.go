// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"sort"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// allowedWrites is the table of every write that stays open while an installation admin is signed in as a user
// (decision 41). Each row names why it may stay open: it changes the files or their metadata, which the audit records
// under both people, or it is anonymous and carries its own credential. A write that sends mail, leaves a notice, or
// records a person's own state is refused instead. TestImpersonationAllowedWrites fails for a write that is open but
// not listed here, and for a listed write that is no longer open, so a new write is classified on purpose.
var allowedWrites = map[string]string{
	"POST /api/v1/auth/password/forgot":               "anonymous; mails only the account's own user, with no session",
	"POST /api/v1/auth/impersonation/end":             "ends the impersonation; it is the way back",
	"POST /api/v1/folders/{fld}/directories":          "file data",
	"POST /api/v1/folders/{fld}/files":                "file data",
	"PATCH /api/v1/nodes/{nod}":                       "file data: rename",
	"DELETE /api/v1/nodes/{nod}":                      "file data: trash",
	"POST /api/v1/nodes/{nod}/move":                   "file data",
	"POST /api/v1/nodes/{nod}/copy":                   "file data; the copy job keeps the admin's mark (F5)",
	"POST /api/v1/nodes/{nod}/shortcuts":              "file data",
	"POST /api/v1/nodes/{nod}/exports":                "file data: a derived copy",
	"POST /api/v1/zips":                               "file data: the zip job keeps the admin's mark (F5)",
	"DELETE /api/v1/zips/{zip}":                       "the caller's own zip job",
	"PUT /api/v1/nodes/{nod}/content":                 "file data",
	"PUT /api/v1/nodes/{nod}/labels/{lbf}":            "file metadata",
	"DELETE /api/v1/nodes/{nod}/labels/{lbf}":         "file metadata",
	"POST /api/v1/nodes/{nod}/restore":                "file data",
	"POST /api/v1/trash/empty":                        "file data",
	"DELETE /api/v1/trash/{nod}":                      "file data",
	"PATCH /api/v1/nodes/{nod}/versions/{ver}":        "file data: version label",
	"POST /api/v1/nodes/{nod}/versions/{ver}/restore": "file data",
	"DELETE /api/v1/nodes/{nod}/versions/{ver}":       "file data",
	"POST /api/v1/notifications/unsubscribe":          "anonymous; the token in the mail is the credential",
	"POST /api/v1/nodes/{nod}/links":                  "a signed link (the viewer's preview), capped at the sign-in-as's end (F8)",
	"POST /api/v1/share/open":                         "anonymous; the visitor's own credential",
	"POST /api/v1/share/renew":                        "anonymous; the visitor's own credential",
	"POST /wopi/files/{nod}/contents":                 "office edit: a WOPI token, not the session",
	"POST /wopi/files/{nod}":                          "office lock: a WOPI token, not the session",
	"POST /api/v1/uploads":                            "file data: an upload",
	"PATCH /api/v1/uploads/{upl}":                     "file data: an upload",
	"DELETE /api/v1/uploads/{upl}":                    "file data: an upload",
	"POST /api/v1/uploads/{upl}/commit":               "file data: an upload",
}

// TestImpersonationAllowedWrites pins the writes that stay open while impersonating (F4). The walk covers every route
// the table classifies impAllow, not only the refusals.
func TestImpersonationAllowedWrites(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	var open []string
	for _, rt := range s.routes() {
		if rt.imp != impAllow || rt.method == "GET" || rt.method == "HEAD" || rt.method == "OPTIONS" {
			continue
		}
		if rt.path == "unmatched" {
			continue
		}
		open = append(open, rt.method+" "+rt.path)
	}
	sort.Strings(open)
	var want []string
	for k := range allowedWrites {
		want = append(want, k)
	}
	sort.Strings(want)
	for _, k := range open {
		if _, ok := allowedWrites[k]; !ok {
			t.Errorf("%q is open while impersonating but not in the table: classify it on purpose", k)
		}
	}
	for _, k := range want {
		found := false
		for _, o := range open {
			if o == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is in the table but no longer open while impersonating", k)
		}
	}
}

// A new document is refused while an installation admin is signed in as a user (decision 91): it is made as the
// person and opens the office editor, which the impersonation does not reach, so the route is refused with the reason
// the other refused writes give. The refusal comes before the folder is read.
func TestImpersonatedDocumentRefused(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	imp, _ := fx.signedInAsMember(t)
	e := wantError(t, callSession(t, fx.s, imp, "POST", "/api/v1/nodes/nod_none/documents", map[string]any{"type": "docx"}), 403, apierr.Forbidden)
	if e.Details["reason"] != reasonImpersonation {
		t.Errorf("reason %v, want %s", e.Details["reason"], reasonImpersonation)
	}
}
