// SPDX-License-Identifier: AGPL-3.0-or-later

// Package recovery holds the steps that issue a verification or reset token and end a user's sessions. The HTTP
// handlers in internal/api and the local admin commands in cmd/selfcloud both use them, so a password reset, a disable
// and a command-line reset end the same things in the same order.
package recovery

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Token is a verification or reset token made for a user. Secret is the token itself. Only its hash is stored.
// Link is the link that carries it.
type Token struct {
	Secret  string
	Link    string
	Expires time.Time
}

// DefaultLifetime returns how long a token of purpose lives and the link template it fills, from the users
// configuration.
func DefaultLifetime(users config.UsersConfig, purpose string) (time.Duration, string) {
	switch purpose {
	case store.PurposeReset:
		return users.ResetTTL.D(), users.ResetURLTemplate
	case store.PurposeEmailChange:
		// A confirmation of a new address lives as long as a verification does (decision 89).
		return users.VerificationTTL.D(), users.EmailChangeURLTemplate
	}
	return users.VerificationTTL.D(), users.VerifyURLTemplate
}

// Issue makes a token of purpose for u, inside tx. It voids the user's earlier tokens of that purpose, so only the
// newest one works.
func Issue(ctx context.Context, tx *store.Tx, u *store.User, purpose string, ttl time.Duration, tmpl, publicURL string,
	now time.Time) (*Token, error) {
	if err := tx.DeleteVerifications(ctx, u.ID, purpose, ""); err != nil {
		return nil, err
	}
	secret, hash := auth.NewToken()
	t := &Token{Secret: secret, Link: TokenLink(tmpl, publicURL, secret), Expires: now.Add(ttl)}
	err := tx.InsertVerification(ctx, &store.Verification{ID: id.New("vrf"), UserID: u.ID,
		Purpose: purpose, TokenHash: hash, ExpiresAt: t.Expires, CreatedAt: now})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// TokenLink fills a link template. The token is URL-escaped; the templates put it in the fragment, so it is never sent
// to a server or written to a log.
func TokenLink(tmpl, publicURL, token string) string {
	return strings.NewReplacer("{public_url}", publicURL, "{token}", url.QueryEscape(token)).Replace(tmpl)
}

// CheckPassword checks a password someone is setting.
func CheckPassword(p string) error {
	if err := auth.ValidPassword(p); err != nil {
		return apierr.New(apierr.InvalidRequest, err.Error())
	}
	return nil
}

// EndUserTokens ends a user's delegated tokens and editing sessions, as handleRevokeUserTokens does. It returns the
// cut-off now in force, which a device cookie made in the same transaction must be stamped after (sec1, decision 84,
// review F9).
func EndUserTokens(ctx context.Context, tx *store.Tx, accountID, userID string) (time.Time, error) {
	cut, err := tx.RevokeUserTokens(ctx, accountID, userID, store.Now())
	if err != nil {
		return time.Time{}, err
	}
	_, err = tx.RevokeWOPISessions(ctx, accountID, userID, cut)
	return cut, err
}

// EndSessions ends every session of u: the impersonations in which u is the person signed in as, the sign-in-as an
// admin of u began, the sessions themselves, and the delegated tokens. reason says why in the audit rows of the
// impersonations. It runs inside tx and returns the token cut-off now in force.
func EndSessions(ctx context.Context, tx *store.Tx, u *store.User, reason string) (time.Time, error) {
	if err := EndImpersonations(ctx, tx, u.ID, reason); err != nil {
		return time.Time{}, err
	}
	if err := EndAdminImpersonations(ctx, tx, u.ID, "admin_sessions_ended"); err != nil {
		return time.Time{}, err
	}
	if err := tx.DeleteUserSessions(ctx, u.ID); err != nil {
		return time.Time{}, err
	}
	return EndUserTokens(ctx, tx, u.AccountID, u.ID)
}

// ImpersonationEnd is the audit entry for an impersonation's end. It is under the admin who began it.
func ImpersonationEnd(sess *store.Session, reason string) *store.AuditEntry {
	return &store.AuditEntry{Time: store.Now(), AccountID: sess.AccountID, ActorKind: string(auth.KindUser),
		ActorID: sess.ImpersonatorID, Action: "impersonation.end", Target: sess.UserID,
		Details: map[string]any{"reason": reason}}
}

// EndImpersonations ends each impersonation in which userID is the person signed in as, and audits each end under the
// admin who began it. It runs inside tx, before the user's sessions are deleted.
func EndImpersonations(ctx context.Context, tx *store.Tx, userID, reason string) error {
	rows, err := tx.ImpersonationsOf(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range rows {
		if err := tx.DeleteSession(ctx, sess.IDHash); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, ImpersonationEnd(sess, reason)); err != nil {
			return err
		}
	}
	return nil
}

// EndAdminImpersonations ends every impersonation the admin began, when the admin is removed, signs out, changes or
// loses its sessions (reason says which). It runs inside tx, before the admin's row is deleted, which would otherwise
// take the impersonations with it unaudited.
func EndAdminImpersonations(ctx context.Context, tx *store.Tx, adminID, reason string) error {
	rows, err := tx.ImpersonationsBy(ctx, adminID)
	if err != nil {
		return err
	}
	for _, sess := range rows {
		if err := tx.DeleteSession(ctx, sess.IDHash); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, ImpersonationEnd(sess, reason)); err != nil {
			return err
		}
	}
	return nil
}
