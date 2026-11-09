// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// Bounds on the admin accounts and admin audit filters, checked at the command line.
const (
	maxAccountNameFilter = 200
	maxExternalIDFilter  = 255
	maxAuditActionFilter = 128
	// listPage is how many accounts one page of the listing reads.
	listPage = 200
)

// hasControl reports whether s holds a control character, a tab or a line break among them.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

// adminAccounts lists the accounts, oldest first, as a tab-separated table: id, name, status, external ID, created (RFC
// 3339, UTC), users (all of them, whatever their status), used bytes and quota bytes. A dash stands for an empty external
// ID and for no quota. It holds no key, and it writes no audit row.
func adminAccounts(args []string, stdout, stderr io.Writer) int {
	var status, name, externalID *string
	cfg, _, code := loadConfig("admin accounts", args, stderr, 0, func(fs *flag.FlagSet) {
		status = fs.String("status", "", "list only the accounts with this status: active or suspended")
		name = fs.String("name", "", "list only the accounts whose name contains this text (case and accents ignored)")
		externalID = fs.String("external-id", "", "list only the account with this external ID")
	})
	if cfg == nil {
		return code
	}
	const cmd = "accounts"
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s\n", cmd, msg)
		return 2
	}
	switch *status {
	case "", store.AccountActive, store.AccountSuspended:
	default:
		return usage("-status must be active or suspended")
	}
	if utf8.RuneCountInString(*name) > maxAccountNameFilter || hasControl(*name) {
		return usage(fmt.Sprintf("-name must be at most %d characters and hold no control character", maxAccountNameFilter))
	}
	if len(*externalID) > maxExternalIDFilter || hasControl(*externalID) {
		return usage(fmt.Sprintf("-external-id must be at most %d bytes and hold no control character", maxExternalIDFilter))
	}

	ctx := context.Background()
	st, c := adminStoreFor(cfg, stderr, cmd)
	if st == nil {
		return c
	}
	defer st.Close()
	// The name filter matches the search form of a name: case and accents ignored, as the name collation ignores them.
	needle := ""
	if *name != "" {
		needle = names.SearchForm(*name)
	}
	fmt.Fprintln(stdout, "id\tname\tstatus\texternal_id\tcreated\tusers\tused_bytes\tquota_bytes")
	filter := store.AccountFilter{ExternalID: *externalID, Status: *status}
	for cursor := ""; ; {
		accs, next, err := st.AccountsFiltered(ctx, filter, store.Page{Limit: listPage, Cursor: cursor})
		if err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", cmd, err)
			return 1
		}
		for _, a := range accs {
			if needle != "" && !strings.Contains(names.SearchForm(a.Name), needle) {
				continue
			}
			users, err := st.CountAccountUsers(ctx, a.ID)
			if err != nil {
				fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", cmd, err)
				return 1
			}
			ext, quota := a.ExternalID, "-"
			if ext == "" {
				ext = "-"
			}
			if a.QuotaBytes != 0 {
				quota = strconv.FormatInt(a.QuotaBytes, 10)
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", cell(a.ID), cell(a.Name), cell(a.Status), cell(ext),
				a.CreatedAt.UTC().Format(time.RFC3339), users, a.UsedBytes, quota)
		}
		if next == "" {
			return 0
		}
		cursor = next
	}
}
