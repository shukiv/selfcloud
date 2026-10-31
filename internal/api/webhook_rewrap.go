// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// The rewrap of webhook secrets (design section 10, slice j4, decision 30). A secret sealed under the previous links key
// still opens while that key file exists. The rewrap seals each such secret again under the current key, so the previous
// key can be removed. A secret that neither key opens is reported. It disables its endpoint only when the caller asks for
// that (the command's -disable-unreadable flag), with reason secret_unreadable. An endpoint so disabled whose secret opens
// again goes to paused with its reason cleared, and does not send until its owner sets it active. It is run by
// `selfcloud admin rewrap-secrets`. It is safe to run again and to interrupt: each batch is one write, and a secret is
// rewritten only if the blob it read is still stored.

// rewrapBatch is how many endpoints one write reads and settles, so the writer is held for about a hundred secrets.
const rewrapBatch = 100

// RewrapCounts is what a rewrap did. It holds counts only: no secret, and no URL.
type RewrapCounts struct {
	Endpoints  int // endpoints read
	Rewrapped  int // secrets sealed again under the current key
	Unreadable int // endpoints whose current secret, or a grace secret that is still in use, cannot be read
	Disabled   int // of those, the endpoints this run disabled; only with the disable flag, and only active or paused ones
	Restored   int // endpoints disabled as secret_unreadable whose secret opens again, now paused
	Expired    int // unreadable copies of a rotation whose grace period has ended; nothing signs with them, so they stay
	Skipped    int // endpoints a rotation changed while this run read them; running again reaches them
}

// RewrapWebhookSecrets rewraps the secrets of every endpoint of every account, at now, and returns the counts. The keys
// come from the configuration and the data directory, as the server reads them, with two differences: a missing current
// key is an error, since a new one would make every secret unreadable, and so is a previous key that the configuration
// names and that is not there, as the server treats it. An error writes nothing. With disable false, nothing is disabled.
func RewrapWebhookSecrets(ctx context.Context, st *store.Store, cfg *config.Config, now time.Time, disable bool) (RewrapCounts, error) {
	current, previous, err := readRewrapKeys(cfg)
	if err != nil {
		return RewrapCounts{}, err
	}
	sealer, err := webhook.NewSealer(current, previous)
	if err != nil {
		return RewrapCounts{}, err
	}
	var c RewrapCounts
	after := ""
	for {
		var batch RewrapCounts
		var n int
		var last string
		err := st.Write(ctx, func(tx *store.Tx) error {
			endpoints, err := tx.WebhookEndpointsAfter(ctx, after, rewrapBatch)
			if err != nil {
				return err
			}
			n = len(endpoints)
			batch = RewrapCounts{}
			for _, e := range endpoints {
				last = e.ID
				if err := rewrapEndpoint(ctx, tx, sealer, e, now, disable, &batch); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return c, err
		}
		c.add(batch)
		if n < rewrapBatch {
			return c, nil
		}
		after = last
	}
}

// add adds the counts of a batch to c.
func (c *RewrapCounts) add(b RewrapCounts) {
	c.Endpoints += b.Endpoints
	c.Rewrapped += b.Rewrapped
	c.Unreadable += b.Unreadable
	c.Disabled += b.Disabled
	c.Restored += b.Restored
	c.Expired += b.Expired
	c.Skipped += b.Skipped
}

// rewrapEndpoint settles the secrets of one endpoint inside tx. The current secret is always checked. A grace secret is
// checked while its grace period runs; after that an unreadable copy is left as it is.
func rewrapEndpoint(ctx context.Context, tx *store.Tx, sealer *webhook.Sealer, e *store.WebhookEndpoint, now time.Time,
	disable bool, c *RewrapCounts) error {
	c.Endpoints++
	unreadable := false

	blob, changed, err := sealer.Rewrap(e.SecretEnc, webhookBinding(e.AccountID, e.ID, webhook.FieldSecret))
	switch {
	case err != nil:
		unreadable = true
	case changed:
		ok, err := tx.SetWebhookSecret(ctx, e.AccountID, e.ID, false, e.SecretEnc, blob)
		if err != nil {
			return err
		}
		if !ok {
			c.Skipped++
			return nil
		}
		c.Rewrapped++
		if err := rewrapAudit(ctx, tx, e, now, map[string]any{"previous": false}); err != nil {
			return err
		}
	}

	if e.PreviousSecretEnc != nil {
		live := e.PreviousSecretUntil != nil && now.Before(*e.PreviousSecretUntil)
		blob, changed, err := sealer.Rewrap(e.PreviousSecretEnc, webhookBinding(e.AccountID, e.ID, webhook.FieldPrevious))
		switch {
		case err != nil && live:
			unreadable = true
		case err != nil:
			c.Expired++
		case changed:
			ok, err := tx.SetWebhookSecret(ctx, e.AccountID, e.ID, true, e.PreviousSecretEnc, blob)
			if err != nil {
				return err
			}
			if !ok {
				c.Skipped++
				return nil
			}
			c.Rewrapped++
			if err := rewrapAudit(ctx, tx, e, now, map[string]any{"previous": true}); err != nil {
				return err
			}
		}
	}

	if !unreadable {
		if e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
			return nil
		}
		restored, err := tx.RestoreUnreadableSecret(ctx, e.AccountID, e.ID, now)
		if err != nil {
			return err
		}
		if restored {
			c.Restored++
			return rewrapAudit(ctx, tx, e, now, map[string]any{"restored": true})
		}
		return nil
	}
	c.Unreadable++
	if !disable {
		return nil
	}
	disabled, err := tx.DisableUnreadableSecret(ctx, e.AccountID, e.ID, now, e.URL)
	if err != nil {
		return err
	}
	if disabled {
		c.Disabled++
	}
	return nil
}

// rewrapAudit records a change to an endpoint's secret or state by the rewrap. The entry names the endpoint and what
// changed, never the secret.
func rewrapAudit(ctx context.Context, tx *store.Tx, e *store.WebhookEndpoint, now time.Time, details map[string]any) error {
	return tx.InsertAudit(ctx, &store.AuditEntry{Time: now, AccountID: e.AccountID, ActorKind: "system", ActorID: "cli",
		Action: "webhook.rewrap", Target: e.ID, Details: details})
}

// readRewrapKeys reads the links keys that the rewrap opens secrets with. It never makes a key, unlike the server. A
// previous key that the configuration names must exist and be readable, as loadLinkKeys requires; the default one may be
// absent, which means the secrets only it opens are unreadable.
func readRewrapKeys(cfg *config.Config) (current, previous []byte, err error) {
	dir := filepath.Join(cfg.DataDir, "keys")
	curPath := cfg.Links.SigningKeyFile
	if curPath == "" {
		curPath = filepath.Join(dir, keyFileName)
	}
	if current, err = readKeyFile(curPath); err != nil {
		return nil, nil, errors.Join(errors.New("the current links key cannot be read; the rewrap makes no key"), err)
	}
	prevPath := cfg.Links.PreviousKeyFile
	configured := prevPath != ""
	if !configured {
		prevPath = filepath.Join(dir, prevKeyName)
	}
	previous, err = readKeyFile(prevPath)
	switch {
	case err == nil:
		return current, previous, nil
	case errors.Is(err, fs.ErrNotExist) && !configured:
		return current, nil, nil
	default:
		return nil, nil, err
	}
}
