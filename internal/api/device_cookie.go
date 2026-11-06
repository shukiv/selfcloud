// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// The device cookie (sec1, decision 84) lets a sign-in from a device the user has used before skip the email's cap
// on failed sign-ins, as the OWASP Authentication Cheat Sheet's device-cookie approach does. A successful sign-in or
// password reset sets it. It is "userID.nonce.issued.expiry.mac" (issued and expiry in Unix time, issued in
// milliseconds), where the MAC is HMAC-SHA256 under a key derived from the links signing key with its own label. It is
// not stored: a cookie is trusted only while its MAC, its expiry and its user check out, and only if it was issued after
// the user's cut-off (store.UserTokensNotBefore). A password change or reset, an admin's set-password and a disable move
// the cut-off, so a cookie from before any of them stops working (sec1, decision 84, review F9). Its own failure budget
// (login.device) stops a stolen cookie from guessing without limit.
const deviceCookieLabel = "selfcloud device cookie v1"

// deviceCookieName is the cookie's name. It has the __Host- prefix when the session cookie does (https only).
func (s *Server) deviceCookieName() string {
	if s.sessionCookie == "selfcloud_session" {
		return "selfcloud_device"
	}
	return "__Host-selfcloud_device"
}

// deviceMAC is the MAC over a cookie's fields, encoded for the cookie.
func (s *Server) deviceMAC(userID, nonce string, issuedMS, expiry int64) (string, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, deviceCookieLabel, 32)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%s|%s|%d|%d", userID, nonce, issuedMS, expiry)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// setDeviceCookie sets a new device cookie for userID, with a fresh nonce, and the configured lifetime. issuedAt is the
// time the caller's transaction made the sign-in (or the reset), taken by the caller and not read from the clock here:
// a cut-off committed after that transaction is later, and voids the cookie (sec1, decision 84, review F9).
func (s *Server) setDeviceCookie(w http.ResponseWriter, userID string, issuedAt time.Time) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	issued := issuedAt.UnixMilli()
	expiry := issuedAt.Add(s.cfg.Sessions.DeviceCookieLifetime.D()).Unix()
	mac, err := s.deviceMAC(userID, nonce, issued, expiry)
	if err != nil {
		return err
	}
	name := s.deviceCookieName()
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    fmt.Sprintf("%s.%s.%d.%d.%s", userID, nonce, issued, expiry, mac),
		Path:     "/",
		MaxAge:   int(s.cfg.Sessions.DeviceCookieLifetime.D().Seconds()),
		HttpOnly: true,
		Secure:   name != "selfcloud_device",
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// trustedDevice returns the nonce of the request's device cookie, and the candidate user it names, when the cookie is
// genuine, unexpired and for one of the candidate users. The nonce keys the device's own failure budget, and the user is
// the one that budget covers (decision 90). Anything else is not trusted.
func (s *Server) trustedDevice(r *http.Request, candidates []*store.User) (string, *store.User, bool) {
	c, err := r.Cookie(s.deviceCookieName())
	if err != nil {
		return "", nil, false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 5 {
		return "", nil, false
	}
	userID, nonce, issuedText, expiryText, mac := parts[0], parts[1], parts[2], parts[3], parts[4]
	issued, err := strconv.ParseInt(issuedText, 10, 64)
	if err != nil {
		return "", nil, false
	}
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil || time.Now().Unix() >= expiry {
		return "", nil, false
	}
	want, err := s.deviceMAC(userID, nonce, issued, expiry)
	if err != nil || !hmac.Equal([]byte(mac), []byte(want)) {
		return "", nil, false
	}
	for _, u := range candidates {
		if u.ID != userID {
			continue
		}
		// A cookie issued at or before the user's cut-off is void: the password was changed or reset, the user was
		// disabled, or an admin set the password, at or after the cookie was made (sec1, decision 84, review F9). The cut-off
		// is in milliseconds, as the cookie's issue time is, so a cookie in the same millisecond is void too.
		cut, err := s.store.UserTokensNotBefore(r.Context(), u.AccountID, u.ID)
		if err != nil || (!cut.IsZero() && issued <= cut.UnixMilli()) {
			return "", nil, false
		}
		return nonce, u, true
	}
	return "", nil, false
}
