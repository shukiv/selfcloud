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
// password reset sets it. It is "userID.nonce.expiry.mac", where the MAC is HMAC-SHA256 under a key derived from the
// links signing key with its own label. It is not stored: a cookie is trusted only while its MAC, its expiry and its
// user check out. Its own failure budget (login.device) stops a stolen cookie from guessing without limit.
const deviceCookieLabel = "selfcloud device cookie v1"

// deviceCookieName is the cookie's name. It has the __Host- prefix when the session cookie does (https only).
func (s *Server) deviceCookieName() string {
	if s.sessionCookie == "selfcloud_session" {
		return "selfcloud_device"
	}
	return "__Host-selfcloud_device"
}

// deviceMAC is the MAC over a cookie's fields, encoded for the cookie.
func (s *Server) deviceMAC(userID, nonce string, expiry int64) (string, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, deviceCookieLabel, 32)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%s|%s|%d", userID, nonce, expiry)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

// setDeviceCookie sets a new device cookie for userID, with a fresh nonce, and the configured lifetime.
func (s *Server) setDeviceCookie(w http.ResponseWriter, userID string) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	expiry := time.Now().Add(s.cfg.Sessions.DeviceCookieLifetime.D()).Unix()
	mac, err := s.deviceMAC(userID, nonce, expiry)
	if err != nil {
		return err
	}
	name := s.deviceCookieName()
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    fmt.Sprintf("%s.%s.%d.%s", userID, nonce, expiry, mac),
		Path:     "/",
		MaxAge:   int(s.cfg.Sessions.DeviceCookieLifetime.D().Seconds()),
		HttpOnly: true,
		Secure:   name != "selfcloud_device",
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// trustedDevice returns the nonce of the request's device cookie when the cookie is genuine, unexpired and for one of
// the candidate users. The nonce keys the device's own failure budget. Anything else is not trusted.
func (s *Server) trustedDevice(r *http.Request, candidates []*store.User) (string, bool) {
	c, err := r.Cookie(s.deviceCookieName())
	if err != nil {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 {
		return "", false
	}
	userID, nonce, expiryText, mac := parts[0], parts[1], parts[2], parts[3]
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil || time.Now().Unix() >= expiry {
		return "", false
	}
	want, err := s.deviceMAC(userID, nonce, expiry)
	if err != nil || !hmac.Equal([]byte(mac), []byte(want)) {
		return "", false
	}
	for _, u := range candidates {
		if u.ID == userID {
			return nonce, true
		}
	}
	return "", false
}
