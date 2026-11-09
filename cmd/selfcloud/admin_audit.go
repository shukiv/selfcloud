// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	defaultAuditLimit = 100
	minAuditLimit     = 1
	maxAuditLimit     = 10000
)

// parseAuditSince reads -since: an RFC 3339 time, or a duration back from now such as 24h. A duration must be positive.
func parseAuditSince(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("-since must be an RFC 3339 time or a positive duration such as 24h")
	}
	return now.Add(-d), nil
}

// auditField makes a value safe for a tab-separated cell: a tab or a line break becomes a space.
func auditField(s string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

// auditDetails prints the stored details as key=value pairs, in key order, and nothing else.
func auditDetails(d map[string]any) string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, auditField(k)+"="+auditField(fmt.Sprint(d[k])))
	}
	return strings.Join(pairs, " ")
}

// adminAudit lists the audit entries of every account and of the installation, newest first, as a tab-separated table:
// time (RFC 3339, UTC), account (a dash for an installation-wide entry), actor (kind:id), action, target and details. The
// filters narrow the list; -limit defaults to 100. It writes no audit row.
func adminAudit(args []string, stdout, stderr io.Writer) int {
	var account, user, action, since *string
	var limit *int
	cfg, _, code := loadConfig("admin audit", args, stderr, 0, func(fs *flag.FlagSet) {
		account = fs.String("account", "", "list only this account's entries (an account ID)")
		user = fs.String("user", "", "list only entries whose actor or target is this user: an ID or an email address")
		action = fs.String("action", "", "list only entries whose action starts with this text")
		since = fs.String("since", "", "list only entries at or after this time: RFC 3339, or a duration back from now such as 24h")
		limit = fs.Int("limit", defaultAuditLimit, "print at most this many entries, newest first (1 to 10000)")
	})
	if cfg == nil {
		return code
	}
	const cmd = "audit"
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s\n", cmd, msg)
		return 2
	}
	if *account != "" && !id.Valid("acc", *account) {
		return usage("-account must be an account ID")
	}
	if *user != "" && !validUserRef(*user) {
		return usage("-user must be a user ID or an email address")
	}
	if len(*action) > maxAuditActionFilter || hasControl(*action) {
		return usage(fmt.Sprintf("-action must be at most %d bytes and hold no control character", maxAuditActionFilter))
	}
	var from time.Time
	if *since != "" {
		t, err := parseAuditSince(*since, time.Now())
		if err != nil {
			return usage(err.Error())
		}
		from = t
	}
	if *limit < minAuditLimit || *limit > maxAuditLimit {
		return usage(fmt.Sprintf("-limit must be from %d to %d", minAuditLimit, maxAuditLimit))
	}

	ctx := context.Background()
	st, c := adminStoreFor(cfg, stderr, cmd)
	if st == nil {
		return c
	}
	defer st.Close()
	q := store.AdminAuditQuery{AccountID: *account, ActionPrefix: *action, Since: from, Limit: *limit}
	if *user != "" {
		ids, err := userIDsFor(ctx, st, *user)
		if err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", cmd, err)
			return 1
		}
		if len(ids) == 0 {
			// No user has this email, so no entry can match. A fresh ID is stored nowhere, so it matches nothing.
			ids = []string{id.New("usr")}
		}
		q.UserIDs = ids
	}
	rows, err := st.AdminAuditEntries(ctx, q)
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", cmd, err)
		return 1
	}
	fmt.Fprintln(stdout, "time\taccount\tactor\taction\ttarget\tdetails")
	for _, e := range rows {
		acc := e.AccountID
		if acc == "" {
			acc = "-"
		}
		target := e.Target
		if target == "" {
			target = "-"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s:%s\t%s\t%s\t%s\n", e.Time.UTC().Format(time.RFC3339), acc,
			auditField(e.ActorKind), auditField(e.ActorID), auditField(e.Action), auditField(target), auditDetails(e.Details))
	}
	return 0
}

// userIDsFor returns the user IDs that ref names: the ID itself, or every user with the email address.
func userIDsFor(ctx context.Context, st *store.Store, ref string) ([]string, error) {
	if id.Valid("usr", ref) {
		return []string{ref}, nil
	}
	rows, err := st.FindUsersByEmail(ctx, strings.ToLower(ref), "", 1000)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, u := range rows {
		ids = append(ids, u.ID)
	}
	return ids, nil
}
