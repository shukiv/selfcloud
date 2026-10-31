// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The routes of notifications (design section 14, decision 43): the signed-in user's own list, unread count, mark-read and
// email switch, and the unsubscribe link of a notification mail. A delegated token never reads or changes them.

const (
	// notifyUnsubLabel labels the key the unsubscribe token is sealed under, so no other token is opened by it.
	notifyUnsubLabel = "selfcloud notify unsubscribe v1"
	// notifyUnsubPrefix starts every unsubscribe token.
	notifyUnsubPrefix = "nsu_"
	// notifyUnsubAAD binds a token to its one purpose: the email switch.
	notifyUnsubAAD = "notify.email"
	// notifyUnsubPath is the route of the unsubscribe page and its one-click POST.
	notifyUnsubPath = "/api/v1/notifications/unsubscribe"
)

// unreadJSON is the bell's count. More is set when the count stopped at notifyUnreadScan, so the bell shows "more".
type unreadJSON struct {
	Count int  `json:"count"`
	More  bool `json:"more,omitempty"`
}

// emailJSON is the email switch of the caller.
type emailJSON struct {
	Email bool `json:"email"`
}

// ownNotifyCaller is the caller of the notification routes: a signed-in user. An account key has no inbox, and a delegated
// token, whatever its scope, is not the user's own sitting at the screen.
func ownNotifyCaller(p *auth.Principal) error {
	if err := userCaller(p, "notifications"); err != nil {
		return err
	}
	// An installation admin signed in as this user reads and changes nothing of the user's inbox (decision 41).
	if p.Impersonator != "" {
		return impersonationRefused()
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot read or change notifications; only the signed-in user can")
	}
	return nil
}

// handleListNotifications lists the caller's notifications, newest first, one page at a time. A notification whose item the
// caller no longer reaches is left out of the page, so a page may come back shorter than its limit, as the recent list does.
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownNotifyCaller(p); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.store.NotificationsPage(ctx, p.AccountID, p.UserID, page)
	if err != nil {
		return storeErr(err)
	}
	items, err := s.visibleNotices(ctx, p, rows)
	if err != nil {
		return err
	}
	out := listResponse[notifyJSON]{Items: items}
	if len(rows) == page.Limit {
		out.NextCursor = store.NotificationCursor(rows[len(rows)-1])
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleUnreadNotifications counts the caller's unread notifications the caller still reaches. It reads at most
// notifyUnreadScan of the newest unread rows, so the count is bounded whatever the history.
func (s *Server) handleUnreadNotifications(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownNotifyCaller(p); err != nil {
		return err
	}
	ctx := r.Context()
	rows, err := s.store.UnreadNewest(ctx, p.AccountID, p.UserID, notifyUnreadScan)
	if err != nil {
		return storeErr(err)
	}
	items, err := s.visibleNotices(ctx, p, rows)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, unreadJSON{Count: len(items), More: len(rows) == notifyUnreadScan})
	return nil
}

// handleReadNotification marks one of the caller's notifications read. Another user's notification, and one of another
// account, are not_found: the write touches the caller's own rows only.
func (s *Server) handleReadNotification(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownNotifyCaller(p); err != nil {
		return err
	}
	nid := r.PathValue("ntf")
	if !id.Valid("ntf", nid) {
		return notFound("notification")
	}
	ctx := r.Context()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.MarkNotificationRead(ctx, p.AccountID, p.UserID, nid, store.Now())
	})
	if errors.Is(err, store.ErrNotFound) {
		return notFound("notification")
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleGetNotifySettings answers the caller's email switch. In-app notifications have no switch: they stay on.
func (s *Server) handleGetNotifySettings(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownNotifyCaller(p); err != nil {
		return err
	}
	on, err := s.store.NotifyEmailOn(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, emailJSON{Email: on})
	return nil
}

// handlePatchNotifySettings sets the caller's email switch. Only the caller's own switch can be set: no route takes another
// user, so an account owner cannot turn email on for a member.
func (s *Server) handlePatchNotifySettings(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownNotifyCaller(p); err != nil {
		return err
	}
	var in struct {
		Email *bool `json:"email"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Email == nil {
		return apierr.New(apierr.InvalidRequest, "email must be true or false")
	}
	ctx := r.Context()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.SetNotifyEmail(ctx, p.AccountID, p.UserID, *in.Email, store.Now())
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, emailJSON{Email: *in.Email})
	return nil
}

// sealUnsubscribe returns the unsubscribe token of a user, for the mail. The token is the user's IDs sealed under a key of its
// own, with the purpose as additional data, so it reveals no one, opens nothing but the email switch, and is refused by every
// other route. It has no expiry: an old mail still works, as the design says.
func (s *Server) sealUnsubscribe(accountID, userID string) (string, error) {
	aead, err := unsubAEAD(s.links.current)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(accountID+"\x00"+userID), []byte(notifyUnsubAAD))
	return notifyUnsubPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openUnsubscribe returns the account and user a token names. It tries the current key and then the previous one, so a
// mail sent before a key rotation still works. A token that does not open is not ok.
func (s *Server) openUnsubscribe(token string) (accountID, userID string, ok bool) {
	body, found := strings.CutPrefix(token, notifyUnsubPrefix)
	if !found {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", "", false
	}
	keys := [][]byte{s.links.current}
	if s.links.previous != nil {
		keys = append(keys, s.links.previous)
	}
	for _, k := range keys {
		aead, err := unsubAEAD(k)
		if err != nil || len(raw) < aead.NonceSize() {
			continue
		}
		plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(notifyUnsubAAD))
		if err != nil {
			continue
		}
		acc, usr, cut := strings.Cut(string(plain), "\x00")
		if !cut || !id.Valid("usr", usr) || acc == "" {
			return "", "", false
		}
		return acc, usr, true
	}
	return "", "", false
}

// unsubscribeURL is the address of a user's unsubscribe, for the List-Unsubscribe header. It is "" when no token can be made,
// and then the mail goes without the header.
func (s *Server) unsubscribeURL(accountID, userID string) string {
	tok, err := s.sealUnsubscribe(accountID, userID)
	if err != nil {
		s.log.Warn("cannot make an unsubscribe address")
		return ""
	}
	return strings.TrimSuffix(s.cfg.PublicURL, "/") + notifyUnsubPath + "?t=" + url.QueryEscape(tok)
}

// unsubAEAD returns the cipher that seals unsubscribe tokens, under a key derived from secret with the token's label.
func unsubAEAD(secret []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, secret, nil, notifyUnsubLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// unsubscribePage is the small page an unsubscribe link opens: a form to confirm, the result, or the refusal. It carries no
// name and no address, and it is in English. It has no script.
var unsubscribePage = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Selfcloud</title>
</head>
<body style="font-family: sans-serif; max-width: 36em; margin: 2em auto; padding: 0 1em;">
{{- if eq .State "confirm"}}
<h1>Stop comment emails</h1>
<p>Selfcloud will stop sending you emails about comments and mentions. Notifications in Selfcloud stay on.</p>
<form method="post" action="{{.Action}}"><button type="submit">Stop these emails</button></form>
{{- else if eq .State "done"}}
<h1>Done</h1>
<p>You will no longer get emails about comments. You can turn them back on in your notification settings.</p>
{{- else}}
<h1>This link is not valid</h1>
<p>Open a newer email from Selfcloud, or turn emails off in your notification settings.</p>
{{- end}}
</body>
</html>
`))

// writeUnsubscribePage writes the page in state, with status. A page is not cached and does not pass the token on in a
// referrer.
func writeUnsubscribePage(w http.ResponseWriter, status int, state, action string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.WriteHeader(status)
	_ = unsubscribePage.Execute(w, struct{ State, Action string }{state, action})
}

// handleUnsubscribeForm answers the page an unsubscribe link opens. It changes nothing: a mail client or a scanner that opens
// the link must not switch the email off, so the switch is made by the POST only.
func (s *Server) handleUnsubscribeForm(w http.ResponseWriter, r *http.Request) error {
	tok := r.URL.Query().Get("t")
	if _, _, ok := s.openUnsubscribe(tok); !ok {
		writeUnsubscribePage(w, http.StatusBadRequest, "invalid", "")
		return nil
	}
	writeUnsubscribePage(w, http.StatusOK, "confirm", notifyUnsubPath+"?t="+url.QueryEscape(tok))
	return nil
}

// handleUnsubscribe switches off a user's email. It takes the form of the page and the one-click POST of RFC 8058 alike, and
// needs no session: the token is the only authority, and no cookie is read, so a page on another site cannot act for a
// user. A valid token for a user who is gone gets the same answer as one for a user who is there, so the answer does not
// tell whether the user exists.
func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) error {
	acc, usr, ok := s.openUnsubscribe(r.URL.Query().Get("t"))
	if !ok {
		writeUnsubscribePage(w, http.StatusBadRequest, "invalid", "")
		return nil
	}
	ctx := r.Context()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.User(ctx, acc, usr); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		}
		return tx.SetNotifyEmail(ctx, acc, usr, false, store.Now())
	})
	if err != nil {
		return err
	}
	writeUnsubscribePage(w, http.StatusOK, "done", "")
	return nil
}
