// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The sign-in link and reset commands run against the real API with the probe account's key.

func init() {
	for name, fn := range userLinkProbes {
		fileProbes[name] = fn
	}
}

// pending makes a person who is waiting for verification and returns the id.
func (p *fileProbe) pending(email string) string {
	p.t.Helper()
	ctx := context.Background()
	now := store.Now()
	u := &store.User{ID: id.New("usr"), AccountID: p.acc, Email: email, EmailKey: strings.ToLower(email), DisplayName: "Probe",
		Locale: "en", Role: store.RoleMember, Status: store.UserPending, CreatedAt: now, UpdatedAt: now}
	if err := p.st.Write(ctx, func(tx *store.Tx) error { return tx.InsertUser(ctx, u) }); err != nil {
		p.t.Fatal(err)
	}
	return u.ID
}

var userLinkProbes = map[string]func(p *fileProbe) []string{
	"user verify": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.pending("verify@example.test"), "--method", "link"}
	},
	"user reset-password": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("reset@example.test"), "--method", "link"}
	},
	"user sign-in-link": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("sign-in@example.test"), "--next", "/files"}
	},
	"user revoke-tokens": func(p *fileProbe) []string {
		return []string{"--account", p.acc, p.member("revoke@example.test"), "--yes"}
	},
}
