// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// mintFor mints a token with the account key and the given request body, and fails the test unless
// the mint succeeds.
func mintFor(t *testing.T, s *Server, key, accID string, body map[string]any) string {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens", key, body)
	wantStatus(t, rec, 201)
	return decode[tokenJSON](t, rec).Token
}

// payloadOf reads a token's payload without checking its MAC. Tests use it to learn the IDs a token
// names; the server never does.
func payloadOf(t *testing.T, tok string) tokenPayload {
	t.Helper()
	body, _, err := splitToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

// retoken rebuilds a token from a payload and a MAC, both as given. A test uses it to pair a changed
// payload with the MAC of the original, or to sign with a key of its own.
func retoken(t *testing.T, pl tokenPayload, mac []byte) string {
	t.Helper()
	b, err := json.Marshal(pl)
	if err != nil {
		t.Fatal(err)
	}
	return auth.TokenPrefix + base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// withPayload returns tok with its payload changed by change and its MAC kept.
func withPayload(t *testing.T, tok string, change func(*tokenPayload)) string {
	t.Helper()
	body, mac, err := splitToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := parsePayload(body)
	if err != nil {
		t.Fatal(err)
	}
	change(&pl)
	return retoken(t, pl, mac)
}

// flipMAC changes one character in the middle of a token's MAC. The middle carries whole bytes;
// changing the last character can leave the decoded bytes unchanged.
func flipMAC(tok string) string {
	i := len(tok) - 20
	c := "A"
	if tok[i] == 'A' {
		c = "B"
	}
	return tok[:i] + c + tok[i+1:]
}

// getWith sends a GET with a bearer credential, which may be a token.
func getWith(t *testing.T, s *Server, cred, p string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "GET", p, cred, nil)
}

// childrenPath is the path that lists a folder's top level.
func childrenPath(fld string) string { return "/api/v1/folders/" + fld + "/children" }

// rotate makes the current signing key the previous one and writes a new current key, as an
// operator does when rotating.
func rotate(t *testing.T, s *Server) {
	t.Helper()
	keyDir := filepath.Join(s.cfg.DataDir, "keys")
	cur := filepath.Join(keyDir, keyFileName)
	if err := os.Rename(cur, filepath.Join(keyDir, prevKeyName)); err != nil {
		t.Fatal(err)
	}
	fresh := make([]byte, linkKeyLen)
	fresh[0] = 1
	if err := os.WriteFile(cur, fresh, 0o600); err != nil {
		t.Fatal(err)
	}
}

// removePrevious deletes the previous signing key, as an operator does once old tokens have expired.
func removePrevious(t *testing.T, s *Server) {
	t.Helper()
	if err := os.Remove(filepath.Join(s.cfg.DataDir, "keys", prevKeyName)); err != nil {
		t.Fatal(err)
	}
}

// disableMember sets the member's status, which the member's tokens and sessions are checked
// against on every use.
func disableMember(t *testing.T, m *memberFixture, status string) {
	t.Helper()
	wantStatus(t, call(t, m.s, "PATCH", "/api/v1/accounts/"+m.acc.ID+"/users/"+m.ann.ID, m.key,
		map[string]any{"status": status}), 200)
}

func TestMintRules(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, acc, key := m.s, m.acc, m.key
	admin := adminKey(t, s)
	mint := "/api/v1/accounts/" + acc.ID + "/tokens"
	body := map[string]any{"folders": []string{m.f.ID}}

	// Only an account key mints: not an installation admin, not a session, and not a token.
	wantError(t, call(t, s, "POST", mint, admin, body), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, m.sess, "POST", mint, body), 403, apierr.Forbidden)
	tok := mintFor(t, s, key, acc.ID, body)
	wantError(t, call(t, s, "POST", mint, tok, body), 403, apierr.Forbidden)

	// A key of another account gets not_found for this account. A folder of another account is
	// not_found too.
	other, otherKey := newAccount(t, s, admin, "Other")
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+other.ID+"/tokens", key, body), 404, apierr.NotFound)
	foreign := newFolder(t, s, otherKey, other.ID, "Theirs")
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{foreign.ID}}), 404, apierr.NotFound)

	// The folder list: at least one, at most 32, each a folder ID.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{}}), 400, apierr.InvalidRequest)
	many := make([]string, tokenMaxFolders+1)
	for i := range many {
		many[i] = m.f.ID
	}
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": many}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{"not-an-id"}}), 400, apierr.InvalidRequest)

	// Lifetime: at least a minute, and at most tokens.max_ttl. A longer one is refused, not cut.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "ttl": 30}), 400, apierr.InvalidRequest)
	rec := call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "ttl": 7200})
	wantError(t, rec, 400, apierr.InvalidRequest)
	if d := decode[errorBody](t, rec).Error.Details["max_ttl_seconds"]; d != float64(3600) {
		t.Errorf("max_ttl_seconds = %v, want 3600", d)
	}
	rec = call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}})
	wantStatus(t, rec, 201)
	if left := time.Until(decode[tokenJSON](t, rec).ExpiresAt); left < 14*time.Minute || left > 15*time.Minute {
		t.Errorf("default lifetime = %v, want 15 minutes", left)
	}

	// Audience and permission are checked, wopi is not available yet, and unknown fields are refused.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "audience": "wopi"}),
		400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "permissions": "admin"}),
		400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "scope": "all"}),
		400, apierr.InvalidRequest)

	// The user must exist in the account, and be active.
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "user": id.New("usr")}),
		404, apierr.NotFound)
	rec = call(t, s, "POST", "/api/v1/accounts/"+acc.ID+"/users", key,
		map[string]any{"email": "pending@example.com", "verification": "link"})
	wantStatus(t, rec, 201)
	pending := decode[userResponse](t, rec)
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "user": pending.ID}),
		403, apierr.UserNotVerified)
	disableMember(t, m, "disabled")
	wantError(t, call(t, s, "POST", mint, key, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID}),
		403, apierr.UserDisabled)

	// The audit log records the mint, and never the token.
	rec = call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"action":"token.mint"`) {
		t.Errorf("no token.mint entry in the audit log: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "sct_") {
		t.Error("the audit log holds a token")
	}
}

func TestTokenFormatAndVerification(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	tok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	if !strings.HasPrefix(tok, auth.TokenPrefix) || len(tok) > tokenMax {
		t.Fatalf("token has the wrong shape: %d bytes", len(tok))
	}
	wantStatus(t, getWith(t, s, tok, childrenPath(m.f.ID)), 200)

	// A token signed by a key this server does not hold.
	strangerKey := tokenKey{mac: make([]byte, tokenMACLen), kid: "AAAAAAAAAAA"}
	strangerKey.mac[0] = 5
	strangerSigned, err := (&tokenKeys{current: strangerKey}).sign(payloadOf(t, tok))
	if err != nil {
		t.Fatal(err)
	}
	// A payload this server signed, with a key ID that is not one of its keys.
	badKID := payloadOf(t, tok)
	badKID.KeyID = "AAAAAAAAAAA"
	badKIDSigned, err := s.tokens.sign(badKID)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"payload changed, MAC kept":    withPayload(t, tok, func(pl *tokenPayload) { pl.Folders = []string{m.g.ID} }),
		"permission changed":           withPayload(t, tok, func(pl *tokenPayload) { pl.Permission = "read" }),
		"user changed":                 withPayload(t, tok, func(pl *tokenPayload) { pl.UserID = id.New("usr") }),
		"MAC changed":                  flipMAC(tok),
		"wrong prefix":                 "sca_" + tok[len(auth.TokenPrefix):],
		"oversize":                     auth.TokenPrefix + strings.Repeat("A", tokenMax),
		"unknown key":                  strangerSigned,
		"unknown kid, validly MACed":   badKIDSigned,
		"no dot":                       strings.Replace(tok, ".", "", 1),
		"two dots":                     tok + ".",
		"bad base64":                   auth.TokenPrefix + "!!!." + strings.Split(tok, ".")[1],
		"empty payload":                auth.TokenPrefix + "." + strings.Split(tok, ".")[1],
		"unknown field, validly MACed": signRaw(t, s, `{"v":1,"extra":true}`),
	}
	for name, bad := range cases {
		rec := getWith(t, s, bad, childrenPath(m.f.ID))
		if rec.Code != 401 || decode[errorBody](t, rec).Error.Code != apierr.Unauthenticated {
			t.Errorf("%s: status %d %s; want 401 unauthenticated", name, rec.Code, rec.Body)
		}
	}

	// A validly signed token past its expiry says so, so a client can refresh it.
	now := time.Now()
	expired := payloadOf(t, tok)
	expired.IssuedAt = now.Add(-2 * time.Hour).UnixMilli()
	expired.ExpiresAt = now.Add(-time.Hour).UnixMilli()
	expired.KeyID = s.tokens.current.kid
	expiredTok, err := s.tokens.sign(expired)
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, getWith(t, s, expiredTok, childrenPath(m.f.ID)), 401, apierr.TokenExpired)
}

// signRaw signs raw JSON as a payload with the current key. The MAC is right, so only the strict
// parse can refuse it.
func signRaw(t *testing.T, s *Server, raw string) string {
	t.Helper()
	body := []byte(raw)
	return auth.TokenPrefix + base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(s.tokens.current.mac, body))
}

func TestTokenSurvivesOneRotationOnly(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s1, key, accID := m.s, m.key, m.acc.ID
	tok := mintFor(t, s1, key, accID, map[string]any{"folders": []string{m.f.ID}})

	// After one rotation the token still verifies, through the previous key.
	rotate(t, s1)
	s2 := newTestServerOn(t, s1)
	wantStatus(t, getWith(t, s2, tok, childrenPath(m.f.ID)), 200)
	fresh := mintFor(t, s2, key, accID, map[string]any{"folders": []string{m.f.ID}})
	wantStatus(t, getWith(t, s2, fresh, childrenPath(m.f.ID)), 200)

	// Once the previous key is gone, the token signed with it fails. A token made with the current
	// key still works.
	removePrevious(t, s1)
	s3 := newTestServerOn(t, s1)
	wantError(t, getWith(t, s3, tok, childrenPath(m.f.ID)), 401, apierr.Unauthenticated)
	wantStatus(t, getWith(t, s3, fresh, childrenPath(m.f.ID)), 200)
}

func TestRevocationEndsTokens(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	admin := adminKey(t, s)
	folder := childrenPath(m.f.ID)
	accTok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	userTok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID})
	wantStatus(t, getWith(t, s, accTok, folder), 200)
	wantStatus(t, getWith(t, s, userTok, folder), 200)

	// Disabling the user ends that user's token. The account key's token is not affected.
	disableMember(t, m, "disabled")
	wantError(t, getWith(t, s, userTok, folder), 401, apierr.Unauthenticated)
	wantStatus(t, getWith(t, s, accTok, folder), 200)
	disableMember(t, m, "active")

	// Suspending the account ends every token while it lasts, and they work again once it is back.
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID, admin, map[string]any{"status": "suspended"}), 200)
	wantError(t, getWith(t, s, accTok, folder), 401, apierr.Unauthenticated)
	wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+accID, admin, map[string]any{"status": "active"}), 200)
	wantStatus(t, getWith(t, s, accTok, folder), 200)

	// Revoking every token issued so far voids them. The short wait keeps the next token's issue
	// time after the cut-off, which is measured in milliseconds.
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens/revoke", key, nil), 200)
	wantError(t, getWith(t, s, accTok, folder), 401, apierr.Unauthenticated)
	wantError(t, getWith(t, s, userTok, folder), 401, apierr.Unauthenticated)
	time.Sleep(2 * time.Millisecond)
	fresh := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	wantStatus(t, getWith(t, s, fresh, folder), 200)

	// Revoking the key that minted a token ends the token, and the key itself.
	k2 := decode[createdKey](t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/keys", admin, map[string]any{"name": "second"}))
	k2tok := mintFor(t, s, k2.Key, accID, map[string]any{"folders": []string{m.f.ID}})
	wantStatus(t, getWith(t, s, k2tok, folder), 200)
	wantStatus(t, call(t, s, "DELETE", "/api/v1/accounts/"+accID+"/keys/"+k2.ID, admin, nil), 204)
	wantError(t, getWith(t, s, k2tok, folder), 401, apierr.Unauthenticated)
	wantStatus(t, getWith(t, s, fresh, folder), 200)
}

func TestTokenScopeNarrowsAccess(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t) // ann: write on f, read on g
	s, key, accID := m.s, m.key, m.acc.ID
	mkdir := func(cred, fld, p string) *httptest.ResponseRecorder {
		return rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/directories", cred, `{"path":"`+p+`","parents":true}`,
			map[string]string{"Content-Type": "application/json"})
	}

	// A folder outside the token's list is not_found, even where the user may reach it.
	onlyF := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID})
	wantError(t, getWith(t, s, onlyF, "/api/v1/nodes/"+m.fileG.ID), 404, apierr.NotFound)
	wantError(t, getWith(t, s, onlyF, childrenPath(m.g.ID)), 404, apierr.NotFound)

	// A token with write, where the grant is read: the level is read.
	writeOnG := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.g.ID}, "user": m.ann.ID, "permissions": "write"})
	wantStatus(t, getWith(t, s, writeOnG, childrenPath(m.g.ID)), 200)
	wantError(t, mkdir(writeOnG, m.g.ID, "/made"), 403, apierr.Forbidden)

	// A token with read, where the grant is write: the level is read.
	readOnF := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID})
	wantStatus(t, getWith(t, s, readOnF, childrenPath(m.f.ID)), 200)
	wantError(t, mkdir(readOnF, m.f.ID, "/made"), 403, apierr.Forbidden)
	wantError(t, rawCall(t, s, "PUT", "/api/v1/nodes/"+m.fileF.ID+"/content", readOnF, "v2",
		map[string]string{"If-Match": m.fileF.ETag}), 403, apierr.Forbidden)

	// A token with write, where the grant is write: the write goes through.
	writeOnF := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	wantStatus(t, mkdir(writeOnF, m.f.ID, "/made"), 201)

	// Listing the folders shows the token's folders only, at the token's level.
	list := decode[listResponse[folderAccessJSON]](t, getWith(t, s, readOnF, "/api/v1/folders"))
	if len(list.Items) != 1 || list.Items[0].ID != m.f.ID || list.Items[0].Access != accessRead {
		t.Errorf("folders seen through a read token = %+v", list.Items)
	}

	// An account-key token reaches its folders with write, and none of the management API.
	accTok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "permissions": "write"})
	wantStatus(t, mkdir(accTok, m.f.ID, "/made-by-key"), 201)
	wantError(t, getWith(t, s, accTok, childrenPath(m.g.ID)), 404, apierr.NotFound)
	for _, p := range []string{
		"/api/v1/accounts/" + accID,
		"/api/v1/accounts/" + accID + "/usage",
		"/api/v1/accounts/" + accID + "/users",
		"/api/v1/accounts/" + accID + "/folders",
		"/api/v1/accounts/" + accID + "/folders/" + m.f.ID + "/grants",
		"/api/v1/accounts/" + accID + "/audit",
	} {
		wantError(t, getWith(t, s, accTok, p), 404, apierr.NotFound)
	}
	wantError(t, getWith(t, s, accTok, "/api/v1/accounts/"+accID+"/keys"), 403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/keys", accTok, map[string]any{"name": "x"}), 403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens", accTok, map[string]any{"folders": []string{m.f.ID}}),
		403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens/revoke", accTok, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "PUT", "/api/v1/accounts/"+accID+"/folders/"+m.f.ID+"/grants/"+m.ann.ID, accTok,
		map[string]any{"level": "read"}), 404, apierr.NotFound)
	wantError(t, getWith(t, s, accTok, "/api/v1/accounts"), 403, apierr.Forbidden)
	wantError(t, getWith(t, s, accTok, "/api/v1/admins"), 403, apierr.Forbidden)
}

func TestUploadContinuesWithAnotherTokenForSameUser(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	tokA := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	upl := uploadID(t, createUpload(t, s, tokA, 6, md("folder", m.f.ID, "name", "new.txt")))
	wantStatus(t, piece(t, s, tokA, upl, 0, "abc", nil), 204)

	// A later token for the same user continues the upload.
	tokB := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	wantStatus(t, piece(t, s, tokB, upl, 3, "def", nil), 204)

	// Another user, granted the same folder, cannot touch the upload.
	vic, _ := verifiedUser(t, s, key, accID, "vic@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+accID+"/folders/"+m.f.ID+"/grants/"+vic.ID, key,
		map[string]any{"level": "write"}), 200)
	tokV := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": vic.ID, "permissions": "write"})
	upl2 := uploadID(t, createUpload(t, s, tokA, 6, md("folder", m.f.ID, "name", "other.txt")))
	wantError(t, piece(t, s, tokV, upl2, 0, "abc", nil), 404, apierr.NotFound)
	wantError(t, headUpload(t, s, tokV, upl2), 404, apierr.NotFound)

	// The audit log names the token's user, and says the action came through a delegated token.
	rec := call(t, s, "GET", "/api/v1/accounts/"+accID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	want := `"via":"delegated_token"`
	if !strings.Contains(rec.Body.String(), want) || !strings.Contains(rec.Body.String(), `"token_key":"`+payloadOf(t, tokA).MintedBy+`"`) {
		t.Errorf("audit entries of token uploads lack the delegation detail: %s", rec.Body)
	}
}

// queueCopy queues a copy of the member's file through tok, and returns the job.
func queueCopy(t *testing.T, m *memberFixture, tok string) *store.Job {
	t.Helper()
	m.s.copySyncNodes = 0 // every copy becomes a job
	rec := call(t, m.s, "POST", "/api/v1/nodes/"+m.fileF.ID+"/copy", tok, map[string]any{"name": "copy.txt"})
	wantStatus(t, rec, 202)
	job, err := m.s.store.Job(context.Background(), path.Base(rec.Header().Get("Location")))
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// childCount counts the top-level entries of the member's folder f, as the account key sees them.
func childCount(t *testing.T, m *memberFixture) int {
	t.Helper()
	return len(decode[listResponse[nodeJSON]](t, call(t, m.s, "GET", childrenPath(m.f.ID), m.key, nil)).Items)
}

func TestQueuedCopyRechecksItsCaller(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	tok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	job := queueCopy(t, m, tok)

	// The caller's tokens are revoked before the job runs. The job fails and writes nothing.
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens/revoke", key, nil), 200)
	if err := s.runCopyJob(context.Background(), job); apierr.From(err).Code != apierr.Forbidden {
		t.Fatalf("copy job after revocation = %v; want forbidden", err)
	}
	if n := childCount(t, m); n != 1 {
		t.Errorf("children after the refused job = %d; want the original 1", n)
	}
}

func TestQueuedCopyIgnoresTokenExpiry(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	// A token for the member that expires a second from now, minted by the account key that really
	// mints tokens for this account.
	minter := payloadOf(t, mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})).MintedBy
	now := time.Now()
	tok, err := s.tokens.sign(tokenPayload{Version: tokenVersion, KeyID: s.tokens.current.kid, AccountID: accID,
		UserID: m.ann.ID, MintedBy: minter, Folders: []string{m.f.ID}, Permission: permWrite, Audience: tokenAudience,
		IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(time.Second).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	job := queueCopy(t, m, tok)
	time.Sleep(1100 * time.Millisecond) // the token has now expired
	if err := s.runCopyJob(context.Background(), job); err != nil {
		t.Fatalf("copy job after its token expired = %v; the copy was allowed and should run", err)
	}
	if n := childCount(t, m); n != 2 {
		t.Errorf("children after the job = %d; want 2", n)
	}
}

func TestCopyJobWithDisabledUserWritesNothing(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	tok := mintFor(t, m.s, m.key, m.acc.ID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"})
	job := queueCopy(t, m, tok)
	disableMember(t, m, "disabled")
	if err := m.s.runCopyJob(context.Background(), job); apierr.From(err).Code != apierr.Forbidden {
		t.Fatalf("copy job for a disabled user = %v; want forbidden", err)
	}
	if n := childCount(t, m); n != 1 {
		t.Errorf("children after the refused job = %d; want the original 1", n)
	}
}

func TestTokenMintRateLimit(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	s.limits["tokens.mint"] = newLimiter(2)
	mint := "/api/v1/accounts/" + acc.ID + "/tokens"
	body := map[string]any{"folders": []string{f.ID}}
	wantStatus(t, call(t, s, "POST", mint, key, body), 201)
	wantStatus(t, call(t, s, "POST", mint, key, body), 201)
	wantError(t, call(t, s, "POST", mint, key, body), 429, apierr.RateLimited)
}

// TestLinkFromTokenEndsWithToken: a signed link outlives every revocation, so one made with a
// delegated token may not outlive the token.
func TestLinkFromTokenEndsWithToken(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	tok := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "ttl": 120})
	exp := time.UnixMilli(payloadOf(t, tok).ExpiresAt)
	links := "/api/v1/nodes/" + m.fileF.ID + "/links"

	// The default link life is longer than the token's, so the link ends when the token does.
	l := decode[linkJSON](t, call(t, s, "POST", links, tok, nil))
	if l.ExpiresAt.After(exp) {
		t.Errorf("link expires %v, after its token (%v)", l.ExpiresAt, exp)
	}
	// Asking for longer is refused, and the reply names the most the token allows.
	e := wantError(t, call(t, s, "POST", links, tok, map[string]any{"ttl": 3600}), 400, apierr.InvalidRequest)
	if got, _ := e.Details["max_ttl_seconds"].(float64); got < 1 || got > 120 {
		t.Errorf("max_ttl_seconds = %v; want at most the token's 120", got)
	}
	// The account key itself is not bound by any token.
	l = decode[linkJSON](t, call(t, s, "POST", links, key, map[string]any{"ttl": 3600}))
	if !l.ExpiresAt.After(exp) {
		t.Errorf("an account key's link expires %v; want its full hour", l.ExpiresAt)
	}
}

// TestRevokeAllNeverMovesBack: a revoke that commits after a later one keeps the later cut-off, so
// tokens the later call ended stay ended.
func TestRevokeAllNeverMovesBack(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	ctx := context.Background()
	later := store.Now().Add(time.Hour)
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		a, err := tx.Account(ctx, accID)
		if err != nil {
			return err
		}
		a.TokensNotBefore = later
		return tx.UpdateAccount(ctx, a)
	}); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, "POST", "/api/v1/accounts/"+accID+"/tokens/revoke", key, nil)
	wantStatus(t, rec, 200)
	if got := decode[map[string]time.Time](t, rec)["tokens_not_before"]; !got.Equal(later) {
		t.Errorf("tokens_not_before = %v; want the later cut-off %v kept", got, later)
	}
	a, err := s.store.Account(ctx, accID)
	if err != nil {
		t.Fatal(err)
	}
	if !a.TokensNotBefore.Equal(later) {
		t.Errorf("stored cut-off = %v; want %v", a.TokensNotBefore, later)
	}
}

// TestCallerReadsTheJobItQueued: the caller a copy runs for may follow its job, under any token for
// the same user; others who do not manage the account may not.
func TestCallerReadsTheJobItQueued(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	s, key, accID := m.s, m.key, m.acc.ID
	body := map[string]any{"folders": []string{m.f.ID}, "user": m.ann.ID, "permissions": "write"}
	job := queueCopy(t, m, mintFor(t, s, key, accID, body))
	jobPath := "/api/v1/jobs/" + job.ID

	wantStatus(t, getWith(t, s, mintFor(t, s, key, accID, body), jobPath), 200)
	wantStatus(t, call(t, s, "GET", jobPath, key, nil), 200) // the account key manages the account
	other := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID}})
	wantError(t, getWith(t, s, other, jobPath), 404, apierr.NotFound)

	// The same user's token for other folders sees nothing of the job; one that also covers the
	// copy's folder does.
	gOnly := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.g.ID}, "user": m.ann.ID})
	wantError(t, getWith(t, s, gOnly, jobPath), 404, apierr.NotFound)
	both := mintFor(t, s, key, accID, map[string]any{"folders": []string{m.f.ID, m.g.ID}, "user": m.ann.ID})
	wantStatus(t, getWith(t, s, both, jobPath), 200)
	wantStatus(t, callSession(t, s, m.sess, "GET", jobPath, nil), 200) // ann's own session has no token scope
}
