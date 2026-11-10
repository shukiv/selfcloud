// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Device sign-in for the command-line client (decision 125, design-cli 3.3).
//
// A terminal asks for a sign-in and gets two codes. The device code stays in the terminal, which polls with it. The user
// code is typed into the browser at /cli-approve by a person who is signed in, and approves the request. The poll then
// returns a device token once. The token is
//
//	scd_ base64url(payload JSON) "." base64url(HMAC-SHA256 of the payload bytes)
//
// The MAC key is derived under a label of its own, so a delegated token never verifies as a device token, nor the reverse,
// and the prefix differs too. A device token names its row in cli_devices, and every use reads that row, so revoking the
// row, its expiry, or a cut-off of its user or account ends the token on its next call.
//
// Only hashes of the device code, of the user code's first half (the selector, which finds the request) and of the whole
// user code are stored. The user code is compared in constant time. A request takes five wrong codes to delete, and it
// expires 600 s after it began; polling does not move that.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	// deviceKeyLabel is the HKDF label of the device token's MAC key. It is not tokenKeyLabel.
	deviceKeyLabel = "selfcloud device token v1"
	deviceVersion  = 1
	// The three labels of the sign-in's hashes: the device code, the user code's selector and the whole user code.
	cliDeviceCodeLabel = "selfcloud cli device code v1"
	cliSelectorLabel   = "selfcloud cli selector v1"
	cliUserCodeLabel   = "selfcloud cli user code v1"
	// cliCodeAlphabet is 20 letters with no vowels and no look-alikes (no I, O or Y): 8 of them are about 34.6 bits.
	cliCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	cliCodeLen      = 8
	// cliDeviceCodeBytes is the random length of a device code.
	cliDeviceCodeBytes = 32
	cliRequestTTL      = 600 * time.Second
	cliIntervalStart   = 5 * time.Second
	cliIntervalStep    = 5 * time.Second
	// cliCodeAttempts is how many wrong codes delete a request.
	cliCodeAttempts = 5
	// cliNameMax bounds the client name a terminal gives.
	cliNameMax = 80
	// cliDefaultName names a terminal that gave none.
	cliDefaultName = "command-line client"
	// cliCodeDrawLimit is the largest byte that maps to the alphabet without bias: 240 = 12 * 20.
	cliCodeDrawLimit = 240
	// The sign-in's limits (cli.start.ip, cli.poll.ip, cli.poll.code and cli.approve in server.go).
	cliStartPerHour      = 10
	cliPollPerIPMinute   = 60
	cliPollPerCodeMinute = 20
	cliApprovePerMinute  = 10
)

// cliCodeKeys holds the three MAC keys of the sign-in's hashes.
type cliCodeKeys struct {
	deviceCode []byte
	selector   []byte
	userCode   []byte
}

// deriveCLICodeKeys derives the sign-in's hash keys from the signing key the links use.
func deriveCLICodeKeys(lk *linkKeys) (*cliCodeKeys, error) {
	dc, err := deriveTokenKey(lk.current, cliDeviceCodeLabel)
	if err != nil {
		return nil, err
	}
	sel, err := deriveTokenKey(lk.current, cliSelectorLabel)
	if err != nil {
		return nil, err
	}
	uc, err := deriveTokenKey(lk.current, cliUserCodeLabel)
	if err != nil {
		return nil, err
	}
	return &cliCodeKeys{deviceCode: dc.mac, selector: sel.mac, userCode: uc.mac}, nil
}

// devicePayload is what a device token says. Its fields are checked after the MAC.
type devicePayload struct {
	Version   int    `json:"v"`
	KeyID     string `json:"kid"` // the signing key that made the MAC
	AccountID string `json:"acc"`
	UserID    string `json:"usr"`
	DeviceID  string `json:"dev"`
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"` // Unix milliseconds
	ExpiresAt int64  `json:"exp"` // Unix milliseconds
}

// valid reports whether the fields have the shape a device token minted here has.
func (pl devicePayload) valid() bool {
	return pl.Version == deviceVersion && id.Valid("acc", pl.AccountID) && id.Valid("usr", pl.UserID) &&
		id.Valid("dev", pl.DeviceID) && pl.Audience == auth.CLIAudience && pl.IssuedAt > 0 && pl.ExpiresAt > pl.IssuedAt
}

// parseDevicePayload reads the payload strictly: an unknown field, or anything after the object, is refused.
func parseDevicePayload(body []byte) (devicePayload, error) {
	var pl devicePayload
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pl); err != nil {
		return pl, errBadToken
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return pl, errBadToken
	}
	return pl, nil
}

// signDevice returns the device token for pl, signed with the current device key.
func (k *tokenKeys) signDevice(pl devicePayload) (string, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return "", fmt.Errorf("device tokens: %w", err)
	}
	return auth.DevicePrefix + base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(tokenMAC(k.current.mac, body)), nil
}

// authenticateDevice identifies the caller from a device token. The shape, the MAC, the payload and the expiry are checked
// first, then one read of the device's row, the account and the user. Every failure but expiry is errBadToken.
func (s *Server) authenticateDevice(ctx context.Context, tok string) (*auth.Principal, error) {
	body, mac, err := splitTokenUnder(auth.DevicePrefix, tok)
	if err != nil {
		return nil, err
	}
	key := s.deviceTokens.match(body, mac)
	if key == nil {
		return nil, errBadToken
	}
	pl, err := parseDevicePayload(body)
	if err != nil || !pl.valid() || pl.KeyID != key.kid {
		return nil, errBadToken
	}
	now := s.store.Clock()
	if now.UnixMilli() >= pl.ExpiresAt {
		return nil, apierr.New(apierr.TokenExpired, "the token has expired; sign in at the terminal again")
	}
	d, err := s.store.DeviceAccess(ctx, pl.AccountID, pl.DeviceID, pl.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadToken
	}
	if err != nil {
		return nil, err
	}
	p := principalOfDevice(pl, d)
	if !callerLive(p, d, now) {
		return nil, errBadToken
	}
	return p, nil
}

// principalOfDevice is the caller a device token stands for: its user, with the full scope of a token that acts as the
// user, at Write, under the cli audience. The must-change flag comes from the same read, so checkImpersonation refuses it.
func principalOfDevice(pl devicePayload, d *store.Delegation) *auth.Principal {
	scope := &auth.TokenScope{
		All:       true,
		Write:     true,
		Audience:  auth.CLIAudience,
		Device:    pl.DeviceID,
		IssuedAt:  time.UnixMilli(pl.IssuedAt).UTC(),
		ExpiresAt: time.UnixMilli(pl.ExpiresAt).UTC(),
	}
	return &auth.Principal{Kind: auth.KindUser, ID: pl.UserID, AccountID: pl.AccountID, UserID: pl.UserID,
		Role: d.UserRole, Token: scope, MustChangePassword: d.UserMustChangePassword}
}

// cliHash is the keyed hash a sign-in stores in place of a code.
func cliHash(key []byte, s string) []byte {
	return tokenMAC(key, []byte(s))
}

// newCLICodes draws a device code and a user code. The user code is shown as two groups of four.
func newCLICodes() (deviceCode, userCode string, err error) {
	var b [cliDeviceCodeBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	deviceCode = base64.RawURLEncoding.EncodeToString(b[:])
	code := make([]byte, 0, cliCodeLen)
	var one [1]byte
	for len(code) < cliCodeLen {
		if _, err := rand.Read(one[:]); err != nil {
			return "", "", err
		}
		if int(one[0]) >= cliCodeDrawLimit {
			continue
		}
		code = append(code, cliCodeAlphabet[int(one[0])%len(cliCodeAlphabet)])
	}
	return deviceCode, string(code[:4]) + "-" + string(code[4:]), nil
}

// normalizeUserCode returns the eight letters of a typed code, in capitals, with the dash and spaces removed. It reports
// false for anything that is not eight letters of the alphabet.
func normalizeUserCode(raw string) (string, bool) {
	code := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, raw))
	if len(code) != cliCodeLen {
		return "", false
	}
	for i := range len(code) {
		if !strings.ContainsRune(cliCodeAlphabet, rune(code[i])) {
			return "", false
		}
	}
	return code, true
}

// cliClientName returns the name a terminal gave, or the default when it gave none. A name is one line of at most
// cliNameMax characters.
func cliClientName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return cliDefaultName, nil
	}
	if len(name) > cliNameMax {
		return "", apierr.Newf(apierr.InvalidRequest, "client_name may be at most %d characters", cliNameMax)
	}
	for i := range len(name) {
		if name[i] < ' ' || name[i] == 0x7f {
			return "", apierr.New(apierr.InvalidRequest, "client_name must be one line of text")
		}
	}
	return name, nil
}

// cliStartRequest is the body of POST /api/v1/auth/cli/start.
type cliStartRequest struct {
	ClientName string `json:"client_name"`
}

// cliStartJSON is the answer to a start. The user code is shown in the terminal, never in the verification URL.
type cliStartJSON struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// handleCLIStart begins a sign-in. It needs no credential, and each address gets ten starts an hour (cli.start.ip).
func (s *Server) handleCLIStart(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("cli.start.ip", addressKey(r)); err != nil {
		return err
	}
	var in cliStartRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := cliClientName(in.ClientName)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var out cliStartJSON
	s.atWrite()
	for attempt := 1; ; attempt++ {
		devCode, userCode, err := newCLICodes()
		if err != nil {
			return err
		}
		now := s.store.Clock()
		plain := strings.ReplaceAll(userCode, "-", "")
		err = s.store.Write(ctx, func(tx *store.Tx) error {
			return tx.InsertCLIRequest(ctx, &store.CLIRequest{
				ID:             id.New("clr"),
				DeviceCodeHash: cliHash(s.cliKeys.deviceCode, devCode),
				SelectorHash:   cliHash(s.cliKeys.selector, plain[:4]),
				CodeHash:       cliHash(s.cliKeys.userCode, plain),
				ClientName:     name,
				ClientIP:       addressKey(r),
				UserAgent:      truncate(r.UserAgent(), 256),
				CreatedAt:      now,
				ExpiresAt:      now.Add(cliRequestTTL),
				Interval:       cliIntervalStart,
				LastPollAt:     now,
			})
		})
		if errors.Is(err, store.ErrConflict) && attempt < 5 {
			continue
		}
		if err != nil {
			return err
		}
		out = cliStartJSON{DeviceCode: devCode, UserCode: userCode, VerificationURL: s.origin + "/cli-approve",
			Interval: int(cliIntervalStart / time.Second), ExpiresIn: int(cliRequestTTL / time.Second)}
		break
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// cliPollRequest is the body of POST /api/v1/auth/cli/poll.
type cliPollRequest struct {
	DeviceCode string `json:"device_code"`
}

// handleCLIPoll asks whether the sign-in was approved. It answers authorization_pending, slow_down or expired_token, and
// once the request is approved it answers the device token, and the request is gone.
func (s *Server) handleCLIPoll(w http.ResponseWriter, r *http.Request) error {
	if err := s.rateLimit("cli.poll.ip", addressKey(r)); err != nil {
		return err
	}
	var in cliPollRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if len(in.DeviceCode) != base64.RawURLEncoding.EncodedLen(cliDeviceCodeBytes) {
		return apierr.New(apierr.InvalidRequest, "device_code is not a device code")
	}
	h := cliHash(s.cliKeys.deviceCode, in.DeviceCode)
	if err := s.rateLimit("cli.poll.code", hex.EncodeToString(h)); err != nil {
		return err
	}
	ctx := r.Context()
	now := s.store.Clock()
	var (
		answer error
		out    tokenJSON
	)
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		row, err := tx.CLIRequestByDeviceHash(ctx, h)
		if errors.Is(err, store.ErrNotFound) {
			answer = expiredSignIn()
			return nil
		}
		if err != nil {
			return err
		}
		if !now.Before(row.ExpiresAt) {
			answer = expiredSignIn()
			return tx.DeleteCLIRequest(ctx, row.ID)
		}
		if row.ApprovedAt == nil {
			// The first poll of a request is due one interval after it began. A poll sooner than the interval is
			// refused, and the interval grows, as RFC 8628 has it.
			if now.Sub(row.LastPollAt) < row.Interval {
				row.Interval += cliIntervalStep
				row.LastPollAt = now
				answer = apierr.New(apierr.SlowDown, "poll less often").With("interval", int(row.Interval/time.Second))
				return tx.UpdateCLIRequest(ctx, row)
			}
			row.LastPollAt = now
			answer = apierr.New(apierr.AuthorizationPending, "the sign-in has not been approved yet")
			return tx.UpdateCLIRequest(ctx, row)
		}
		// The approver is checked again: a password change or a disable after the approval stops the device.
		if err := checkCLIApprover(ctx, tx, row.AccountID, row.UserID); err != nil {
			answer = err
			return tx.DeleteCLIRequest(ctx, row.ID)
		}
		dev := &store.CLIDevice{ID: id.New("dev"), AccountID: row.AccountID, UserID: row.UserID,
			ClientName: row.ClientName, ClientIP: row.ClientIP, UserAgent: row.UserAgent,
			CreatedAt: now, ExpiresAt: now.Add(s.cfg.CLI.DeviceTokenTTL.D())}
		if err := tx.InsertCLIDevice(ctx, dev); err != nil {
			return err
		}
		pl := devicePayload{Version: deviceVersion, KeyID: s.deviceTokens.current.kid, AccountID: dev.AccountID,
			UserID: dev.UserID, DeviceID: dev.ID, Audience: auth.CLIAudience,
			IssuedAt: dev.CreatedAt.UnixMilli(), ExpiresAt: dev.ExpiresAt.UnixMilli()}
		token, err := s.deviceTokens.signDevice(pl)
		if err != nil {
			return err
		}
		out = tokenJSON{Token: token, ExpiresAt: dev.ExpiresAt}
		if err := tx.DeleteCLIRequest(ctx, row.ID); err != nil {
			return err
		}
		e := s.auditEntry(r, dev.AccountID, "cli.sign_in", dev.ID, map[string]any{
			"client_name": dev.ClientName, "expires_at": dev.ExpiresAt.Format(time.RFC3339)})
		e.ActorKind, e.ActorID = string(auth.KindUser), dev.UserID
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	if answer != nil {
		return answer
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
	return nil
}

// expiredSignIn is the answer to a device code that is unknown, spent or past its time. The same answer covers all three.
func expiredSignIn() error {
	return apierr.New(apierr.ExpiredToken, "the sign-in has ended; start it again at the terminal")
}

// checkCLIApprover refuses a person who may not hold a device token: an account that is not active, a user that is not
// verified or is disabled, and a user whose password must change (decision 116's rule for tokens, applied when the sign-in
// is approved and again when it is claimed).
func checkCLIApprover(ctx context.Context, tx *store.Tx, accountID, userID string) error {
	acc, err := tx.Account(ctx, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Forbidden, "this account cannot sign in a terminal")
	}
	if err != nil {
		return err
	}
	switch acc.Status {
	case store.AccountSuspended:
		return apierr.New(apierr.AccountSuspended, "the account is suspended")
	case store.AccountActive:
	default:
		return apierr.New(apierr.Forbidden, "this account cannot sign in a terminal")
	}
	u, err := tx.User(ctx, accountID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return apierr.New(apierr.Forbidden, "this person cannot sign in a terminal")
	}
	if err != nil {
		return err
	}
	switch u.Status {
	case store.UserPending:
		return apierr.New(apierr.UserNotVerified, "the user is not verified yet")
	case store.UserDisabled:
		return apierr.New(apierr.UserDisabled, "the user is disabled")
	}
	if u.MustChangePassword {
		return apierr.New(apierr.Forbidden, "change your password first").With("reason", reasonPasswordChangeRequired)
	}
	return nil
}

// cliCodeRequest is the body of the lookup and approve routes: the code the terminal shows.
type cliCodeRequest struct {
	UserCode string `json:"user_code"`
}

// cliRequestJSON shows a pending request to the person who may approve it.
type cliRequestJSON struct {
	ClientName string    `json:"client_name"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// handleCLILookup shows the request a typed code names, so the person can check it before approving.
func (s *Server) handleCLILookup(w http.ResponseWriter, r *http.Request) error {
	return s.cliCodeAction(w, r, false)
}

// handleCLIApprove approves the request a typed code names, for the signed-in person whose browser it is.
func (s *Server) handleCLIApprove(w http.ResponseWriter, r *http.Request) error {
	return s.cliCodeAction(w, r, true)
}

// cliCodeAction is the browser half of the sign-in: it checks the typed code and either shows the request or approves it.
// A wrong code is counted against the request it names, and the fifth deletes that request. A code whose first half
// names no request is refused the same way, without a count. Both browser routes are limited per session and per address
// (cli.approve).
func (s *Server) cliCodeAction(w http.ResponseWriter, r *http.Request, approve bool) error {
	p := principalOf(r)
	if p == nil || p.Kind != auth.KindUser || p.Token != nil {
		return apierr.New(apierr.Forbidden, "sign in in the browser to approve a terminal")
	}
	if p.Role == auth.RoleAdmin {
		return apierr.New(apierr.Forbidden, "an installation admin cannot sign in at a terminal; use an admin key")
	}
	if p.MustChangePassword {
		return apierr.New(apierr.Forbidden, "change your password first").With("reason", reasonPasswordChangeRequired)
	}
	var sessKey string
	if info := infoFrom(r.Context()); info != nil && info.session != nil {
		sessKey = hex.EncodeToString(info.session.IDHash)
	}
	if err := s.chargeAll(charge{"cli.approve", "session:" + sessKey}, charge{"cli.approve", "ip:" + addressKey(r)}); err != nil {
		return err
	}
	var in cliCodeRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	code, ok := normalizeUserCode(in.UserCode)
	if !ok {
		return apierr.New(apierr.InvalidRequest, "the code is eight letters, as the terminal shows it").With("reason", "malformed_code")
	}
	selector := cliHash(s.cliKeys.selector, code[:4])
	want := cliHash(s.cliKeys.userCode, code)
	ctx := r.Context()
	now := s.store.Clock()
	var (
		answer error
		out    any
	)
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		row, err := tx.CLIRequestBySelector(ctx, selector)
		if errors.Is(err, store.ErrNotFound) {
			answer = wrongCode()
			return nil
		}
		if err != nil {
			return err
		}
		if !hmac.Equal(row.CodeHash, want) {
			row.Attempts++
			if row.Attempts >= cliCodeAttempts {
				answer = wrongCode()
				return tx.DeleteCLIRequest(ctx, row.ID)
			}
			answer = wrongCode()
			return tx.UpdateCLIRequest(ctx, row)
		}
		if !now.Before(row.ExpiresAt) {
			answer = apierr.New(apierr.InvalidRequest, "the sign-in has expired; start it again at the terminal").With("reason", "expired")
			return tx.DeleteCLIRequest(ctx, row.ID)
		}
		if !approve {
			out = cliRequestJSON{ClientName: row.ClientName, ClientIP: row.ClientIP, UserAgent: row.UserAgent,
				CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
			return nil
		}
		if row.ApprovedAt != nil {
			answer = apierr.New(apierr.Conflict, "this sign-in is already approved").With("reason", "already_approved")
			return nil
		}
		if err := checkCLIApprover(ctx, tx, p.AccountID, p.UserID); err != nil {
			answer = err
			return nil
		}
		row.ApprovedAt = &now
		row.AccountID = p.AccountID
		row.UserID = p.UserID
		if err := tx.UpdateCLIRequest(ctx, row); err != nil {
			return err
		}
		e := s.auditEntry(r, p.AccountID, "cli.approve", p.UserID, map[string]any{"client_name": row.ClientName})
		out = map[string]string{"status": "approved"}
		return tx.InsertAudit(ctx, e)
	})
	if err != nil {
		return err
	}
	if answer != nil {
		return answer
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// wrongCode is the answer to a code that names no request, or the wrong code of one. Every wrong code gets this answer, so
// the answer shows nothing about which requests exist.
func wrongCode() error {
	return apierr.New(apierr.InvalidRequest, "that code is not right").With("reason", "wrong_code")
}

// handleCLIRevoke ends the device token that calls it. Only a device token may, and sign-out may call it even when the
// user's password must change.
func (s *Server) handleCLIRevoke(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if p == nil || p.Token == nil || p.Token.Audience != auth.CLIAudience || p.Token.Device == "" {
		return apierr.New(apierr.Forbidden, "only a device token ends itself here")
	}
	ctx := r.Context()
	now := s.store.Clock()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeCLIDevice(ctx, p.AccountID, p.UserID, p.Token.Device, now); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "cli.device_revoke", p.Token.Device, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// cliDeviceJSON is one device in the Manage list. Its token is never shown.
// isDeviceToken reports whether p acts with a terminal's device token (decision 125). Such a token has no minting key, so
// MintedBy is empty for it; the audience is what tells it apart.
func isDeviceToken(p *auth.Principal) bool {
	return p != nil && p.Token != nil && p.Token.Audience == auth.CLIAudience
}

// delegatedOrigin reports whether a token's rights come from something other than the person's own browser session: a
// token minted by an account key, or a device token. Such a token never gets Save As, and the editor's own checks treat
// it as a delegated one (review H1).
func delegatedOrigin(t *auth.TokenScope) bool {
	return t != nil && (t.MintedBy != "" || t.Audience == auth.CLIAudience)
}

type cliDeviceJSON struct {
	ID         string    `json:"id"`
	ClientName string    `json:"client_name"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// handleCLIDevices lists the signed-in person's devices that still work.
func (s *Server) handleCLIDevices(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	devices, err := s.store.CLIDevicesOf(r.Context(), p.AccountID, p.UserID, s.store.Clock())
	if err != nil {
		return err
	}
	out := struct {
		Devices []cliDeviceJSON `json:"devices"`
	}{Devices: []cliDeviceJSON{}}
	for _, d := range devices {
		out.Devices = append(out.Devices, cliDeviceJSON{ID: d.ID, ClientName: d.ClientName, ClientIP: d.ClientIP,
			UserAgent: d.UserAgent, CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// handleCLIDeviceRevoke ends one of the signed-in person's devices, from Manage.
func (s *Server) handleCLIDeviceRevoke(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	dev := r.PathValue("dev")
	if p.AccountID == "" || !id.Valid("dev", dev) {
		return notFound("device")
	}
	ctx := r.Context()
	now := s.store.Clock()
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeCLIDevice(ctx, p.AccountID, p.UserID, dev, now); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return notFound("device")
			}
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "cli.device_revoke", dev, nil))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
