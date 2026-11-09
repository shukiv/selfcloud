// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// refusalLines returns the log lines at level that name a refused WOPI call.
func refusalLines(logs, level string) []string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level="+level) && strings.Contains(line, "wopi request refused") {
			out = append(out, line)
		}
	}
	return out
}

// tokenParts are the parts of an access token a log line must never hold: the token, its body, its signature.
func tokenParts(tok string) []string {
	body, mac, _ := strings.Cut(strings.TrimPrefix(tok, auth.TokenPrefix), ".")
	return []string{tok, body, mac, "access_token"}
}

// lk1 F4: a WOPI call refused at its checks writes one line at warn, which names the reason, the node and the WOPI session,
// and never the token, its body or its signature. A refusal from the same session for the same reason repeats at debug
// within the window (wopi.session_refusal), so a stuck editor cannot fill the log.
func TestWOPIRefusedCallLogsItsReasonAndNeverTheToken(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		prepare func(t *testing.T, fx *officeFixture, tok string) string // returns the token the call sends
		path    func(fx *officeFixture) string
		status  int
		reason  string
	}{
		"ended browser session": {
			prepare: func(t *testing.T, fx *officeFixture, tok string) string {
				wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))
				return tok
			},
			path:   func(fx *officeFixture) string { return nodeRoute(fx.doc.ID) },
			status: 401, reason: "ended browser session",
		},
		"expired token": {
			prepare: func(t *testing.T, fx *officeFixture, tok string) string {
				exp := time.UnixMilli(payloadOf(t, tok).ExpiresAt)
				fx.s.store.SetClock(func() time.Time { return exp.Add(time.Second) })
				return tok
			},
			path:   func(fx *officeFixture) string { return nodeRoute(fx.doc.ID) },
			status: 401, reason: "expired token",
		},
		"ended session": {
			prepare: func(t *testing.T, fx *officeFixture, tok string) string {
				if err := fx.s.store.Write(t.Context(), func(tx *store.Tx) error {
					_, err := tx.RevokeWOPISessions(t.Context(), fx.acc.ID, "", time.Now())
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return tok
			},
			path:   func(fx *officeFixture) string { return nodeRoute(fx.doc.ID) },
			status: 401, reason: "ended session",
		},
		"bad signature": {
			prepare: func(t *testing.T, fx *officeFixture, tok string) string {
				return tamperSignature(t, tok)
			},
			path:   func(fx *officeFixture) string { return nodeRoute(fx.doc.ID) },
			status: 401, reason: "bad signature",
		},
		"wrong node": {
			prepare: func(t *testing.T, fx *officeFixture, tok string) string { return tok },
			path: func(fx *officeFixture) string {
				other := putNode(t, fx.s, fx.key, fx.fld.ID, "/other.docx", "other")
				return nodeRoute(other.ID)
			},
			status: 404, reason: "wrong node",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newOfficeFixture(t)
			tok := fx.openToken(t, "edit")
			sent := c.prepare(t, fx, tok)
			var logs bytes.Buffer
			fx.s.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			wantStatus(t, fx.wopiCall(t, "GET", c.path(fx), sent, nil, nil), c.status)
			warns := refusalLines(logs.String(), "WARN")
			if len(warns) != 1 {
				t.Fatalf("warn lines for one refused call = %d, want 1:\n%s", len(warns), logs.String())
			}
			if !strings.Contains(warns[0], `reason="`+c.reason+`"`) {
				t.Errorf("the line does not name the reason %q:\n%s", c.reason, warns[0])
			}
			if c.reason == "bad signature" {
				// The MAC did not verify, so the payload is not ours to name: the line has no node and no session.
				if !strings.Contains(warns[0], `node="" wopi_session=""`) {
					t.Errorf("a token that did not verify named its node or session:\n%s", warns[0])
				}
			} else if !strings.Contains(warns[0], fx.doc.ID) || !strings.Contains(warns[0], payloadOf(t, tok).Session) {
				t.Errorf("the line does not name the node %s and the WOPI session %s:\n%s", fx.doc.ID,
					payloadOf(t, tok).Session, warns[0])
			}
			for _, part := range tokenParts(sent) {
				if part != "" && strings.Contains(logs.String(), part) {
					t.Errorf("the log holds %q of the token:\n%s", part, logs.String())
				}
			}
		})
	}
}

// lk1 F4: from one session and reason, a repeated refusal is debug within the window, so one warn line stands for the
// first of them.
func TestWOPIRefusedCallsWarnOncePerSessionAndReason(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	wantStatus2xx(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/auth/logout", nil))
	var logs bytes.Buffer
	fx.s.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for i := 0; i < 5; i++ {
		wantStatus(t, fx.wopiCall(t, "GET", nodeRoute(fx.doc.ID), tok, nil, nil), 401)
	}
	if n := len(refusalLines(logs.String(), "WARN")); n != 1 {
		t.Errorf("warn lines for five refusals of one session = %d, want 1", n)
	}
	if n := len(refusalLines(logs.String(), "DEBUG")); n != 4 {
		t.Errorf("debug lines for the four repeats = %d, want 4", n)
	}
}

// tamperSignature returns tok with its signature changed: one bit of the decoded MAC is flipped and the MAC is encoded
// again. The last character is not changed, since its filler bits are ignored by the decoder (lk1 review fix).
func tamperSignature(t *testing.T, tok string) string {
	t.Helper()
	body, _, ok := strings.Cut(strings.TrimPrefix(tok, auth.TokenPrefix), ".")
	if !ok {
		t.Fatalf("token has no signature part")
	}
	raw := macOf(t, tok)
	raw[0] ^= 1
	return auth.TokenPrefix + body + "." + base64.RawURLEncoding.EncodeToString(raw)
}

// macOf returns the decoded signature of tok, as the server compares it.
func macOf(t *testing.T, tok string) []byte {
	t.Helper()
	_, mac, ok := strings.Cut(strings.TrimPrefix(tok, auth.TokenPrefix), ".")
	if !ok {
		t.Fatalf("token has no signature part")
	}
	raw, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// lk1 review fix: a tampered token must carry a different signature, or the refusal test cannot tell a forgery from a
// valid token. A raw base64url string's last character has filler bits the decoder ignores, so "...A" and "...B" can decode
// to one MAC. Here a token ending in "A" is tampered, and the MAC must still change.
func TestWOPITamperedSignatureChangesTheMAC(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	// One real open gives the payload. The tokens below are signed in process by the same signer the open route uses,
	// so the loop makes no HTTP call and spends no office.open rate limit (tf1: the old loop of opens hit the limit).
	pl := payloadOf(t, fx.openToken(t, "edit"))
	tok := ""
	for i := 0; i < 4000 && !strings.HasSuffix(tok, "A"); i++ {
		pl.IssuedAt++
		signed, err := fx.s.wopiTokens.sign(pl)
		if err != nil {
			t.Fatal(err)
		}
		tok = signed
	}
	if !strings.HasSuffix(tok, "A") {
		t.Fatal("no token ending in A was minted in 4000 tries")
	}
	if bytes.Equal(macOf(t, tamperSignature(t, tok)), macOf(t, tok)) {
		t.Fatalf("the tampered token decodes to the same signature as the original; it would still be valid")
	}
}
