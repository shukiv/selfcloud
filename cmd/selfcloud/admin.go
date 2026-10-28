package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"selfcloud/internal/auth"
	"selfcloud/internal/config"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
)

const adminUsage = `Usage: selfcloud admin <command> [-config FILE] [flags]

These commands work on the database directly, so they need no key. They are safe to run
while the server is up.

Commands:
  create-key [-name NAME]        create an installation admin key and print it (shown once)
  list-keys                      list installation admin keys
  revoke-key KEY_ID              revoke an installation admin key
  generate-key                   print a new admin key without storing it, for admin_key_file
  create-account -name NAME [-external-id ID] [-quota SIZE]
                                 create an account and an account key; print both
  create-admin -email EMAIL [-name NAME]
                                 create a pending admin user; print its set-password link and token
  recount-usage [-account ID]    recompute usage and reserved bytes from the files and uploads
                                 (all accounts unless -account is given), and print the change
`

// cliActor is the audit log's name for these commands.
const cliActor = "cli"

func admin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "create-key":
		return adminCreateKey(args, stdout, stderr)
	case "list-keys":
		return adminListKeys(args, stdout, stderr)
	case "revoke-key":
		return adminRevokeKey(args, stdout, stderr)
	case "generate-key":
		secret, _, _ := auth.NewKey(auth.AdminKeyPrefix)
		fmt.Fprintln(stdout, secret)
		return 0
	case "create-account":
		return adminCreateAccount(args, stdout, stderr)
	case "create-admin":
		return adminCreateAdmin(args, stdout, stderr)
	case "recount-usage":
		return adminRecountUsage(args, stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, adminUsage)
		return 0
	}
	fmt.Fprintf(stderr, "selfcloud admin: unknown command %q\n\n%s", cmd, adminUsage)
	return 2
}

// adminStore loads the configuration and opens the database for an admin command.
func adminStore(name string, args []string, stderr io.Writer, nargs int, setup func(*flag.FlagSet)) (
	*store.Store, []string, int) {
	cfg, rest, code := loadConfig("admin "+name, args, stderr, nargs, setup)
	if cfg == nil {
		return nil, nil, code
	}
	log := newLogger(config.LogConfig{Level: "warn", Format: cfg.Log.Format}, stderr)
	st, err := openStore(context.Background(), cfg, log)
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return nil, nil, 1
	}
	return st, rest, 0
}

func cliAudit(accountID, action, target string, details map[string]any) *store.AuditEntry {
	return &store.AuditEntry{Time: store.Now(), AccountID: accountID, ActorKind: string(auth.KindSystem),
		ActorID: cliActor, Action: action, Target: target, Details: details}
}

func adminCreateKey(args []string, stdout, stderr io.Writer) int {
	var name *string
	st, _, code := adminStore("create-key", args, stderr, 0, func(fs *flag.FlagSet) {
		name = fs.String("name", "admin", "a label for the key")
	})
	if st == nil {
		return code
	}
	defer st.Close()
	label := strings.TrimSpace(*name)
	if label == "" {
		fmt.Fprintln(stderr, "selfcloud admin create-key: -name is empty")
		return 2
	}
	secret, prefix, hash := auth.NewKey(auth.AdminKeyPrefix)
	key := &store.APIKey{ID: id.New("key"), Name: label, Prefix: prefix, Hash: hash, CreatedAt: store.Now()}
	ctx := context.Background()
	err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAPIKey(ctx, key); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, cliAudit("", "admin_key.create", key.ID, map[string]any{"name": label}))
	})
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin create-key:", err)
		return 1
	}
	fmt.Fprintln(stdout, secret)
	fmt.Fprintf(stderr, "Created admin key %s (%s). It is shown only once: store it now.\n", key.ID, label)
	return 0
}

func adminListKeys(args []string, stdout, stderr io.Writer) int {
	st, _, code := adminStore("list-keys", args, stderr, 0, nil)
	if st == nil {
		return code
	}
	defer st.Close()
	keys, err := st.APIKeys(context.Background(), "")
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin list-keys:", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPREFIX\tNAME\tCREATED\tSTATE\tLAST USED")
	now := time.Now()
	for _, k := range keys {
		state := "active"
		switch {
		case k.RevokedAt != nil:
			state = "revoked"
		case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
			state = "expired"
		}
		used := "never"
		if k.LastUsedAt != nil {
			used = k.LastUsedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s…\t%s\t%s\t%s\t%s\n", k.ID, k.Prefix, k.Name,
			k.CreatedAt.Format(time.RFC3339), state, used)
	}
	tw.Flush()
	return 0
}

func adminRevokeKey(args []string, stdout, stderr io.Writer) int {
	st, rest, code := adminStore("revoke-key", args, stderr, 1, nil)
	if st == nil {
		return code
	}
	defer st.Close()
	keyID := rest[0]
	ctx := context.Background()
	err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.RevokeAPIKey(ctx, "", keyID, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, cliAudit("", "admin_key.revoke", keyID, nil))
	})
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "selfcloud admin revoke-key: no admin key %q\n", keyID)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin revoke-key:", err)
		return 1
	}
	fmt.Fprintf(stdout, "revoked %s\n", keyID)
	return 0
}

func adminCreateAccount(args []string, stdout, stderr io.Writer) int {
	var name, ext *string
	var quota config.ByteSize
	st, _, code := adminStore("create-account", args, stderr, 0, func(fs *flag.FlagSet) {
		name = fs.String("name", "", "the account's name (required)")
		ext = fs.String("external-id", "", "the client's own reference for the account")
		fs.TextVar(&quota, "quota", config.ByteSize(0), "storage quota, e.g. 50GiB (0 = unlimited)")
	})
	if st == nil {
		return code
	}
	defer st.Close()
	label := strings.TrimSpace(*name)
	if label == "" {
		fmt.Fprintln(stderr, "selfcloud admin create-account: -name is required")
		return 2
	}
	now := store.Now()
	acc := &store.Account{ID: id.New("acc"), ExternalID: *ext, Name: label, Status: store.AccountActive,
		QuotaBytes: int64(quota), CreatedAt: now, UpdatedAt: now}
	secret, prefix, hash := auth.NewKey(auth.AccountKeyPrefix)
	key := &store.APIKey{ID: id.New("key"), AccountID: acc.ID, Name: "created by selfcloud admin",
		Prefix: prefix, Hash: hash, CreatedAt: now}
	ctx := context.Background()
	err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		if err := tx.InsertAPIKey(ctx, key); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, cliAudit(acc.ID, "account.create", acc.ID,
			map[string]any{"name": label, "external_id": *ext, "quota_bytes": acc.QuotaBytes})); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, cliAudit(acc.ID, "key.create", key.ID, map[string]any{"name": key.Name}))
	})
	if errors.Is(err, store.ErrConflict) {
		fmt.Fprintf(stderr, "selfcloud admin create-account: an account with external ID %q exists\n", *ext)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin create-account:", err)
		return 1
	}
	fmt.Fprintf(stdout, "account_id=%s\naccount_key=%s\n", acc.ID, secret)
	fmt.Fprintln(stderr, "The account key is shown only once: store it now.")
	return 0
}

// bootstrapAdminKey adds the admin key held in path unless it is already known. A known key
// that was revoked stays revoked.
func bootstrapAdminKey(ctx context.Context, st *store.Store, path string, log *slog.Logger) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	secret := strings.TrimSpace(string(b))
	if !auth.ValidKeyFormat(auth.AdminKeyPrefix, secret) {
		return fmt.Errorf("%s does not hold an admin key; make one with `selfcloud admin generate-key`", path)
	}
	hash := auth.HashKey(secret)
	if _, err := st.APIKeyByHash(ctx, hash); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	key := &store.APIKey{ID: id.New("key"), Name: "admin_key_file", Prefix: auth.DisplayPrefix(auth.AdminKeyPrefix, secret),
		Hash: hash, CreatedAt: store.Now()}
	err = st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAPIKey(ctx, key); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, cliAudit("", "admin_key.create", key.ID, map[string]any{"name": key.Name}))
	})
	if errors.Is(err, store.ErrConflict) {
		return nil // another process added it first
	}
	if err == nil {
		log.Info("added the admin key from admin_key_file", "key", key.ID)
	}
	return err
}

// adminCreateAdmin makes a pending admin user and prints the link that sets its password. The
// admin completes it with POST /api/v1/auth/verify, so the link works the same as one made for
// any user. The token is printed once and stored only as a hash.
func adminCreateAdmin(args []string, stdout, stderr io.Writer) int {
	var email, name *string
	cfg, _, code := loadConfig("admin create-admin", args, stderr, 0, func(fs *flag.FlagSet) {
		email = fs.String("email", "", "the admin's email address (required)")
		name = fs.String("name", "", "the admin's display name")
	})
	if cfg == nil {
		return code
	}
	addr := strings.TrimSpace(*email)
	parsed, err := netmail.ParseAddress(addr)
	if addr == "" || err != nil || parsed.Address != addr {
		fmt.Fprintln(stderr, "selfcloud admin create-admin: -email must be a bare address such as name@example.com")
		return 2
	}
	label := strings.TrimSpace(*name)
	log := newLogger(config.LogConfig{Level: "warn", Format: cfg.Log.Format}, stderr)
	st, err := openStore(context.Background(), cfg, log)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin create-admin:", err)
		return 1
	}
	defer st.Close()

	secret, hash := auth.NewToken()
	// The same fill as api.Server.tokenLink, which this command cannot reach. The token sits in
	// the fragment, as the templates intend.
	link := strings.NewReplacer("{public_url}", cfg.PublicURL, "{token}", url.QueryEscape(secret)).
		Replace(cfg.Users.VerifyURLTemplate)
	now := store.Now()
	key := strings.ToLower(addr)
	u := &store.User{ID: id.New("usr"), Email: addr, EmailKey: key, DisplayName: label, Locale: "en",
		Role: store.RoleAdmin, Status: store.UserPending, CreatedAt: now, UpdatedAt: now}
	expires := now.Add(cfg.Users.VerificationTTL.D())
	ctx := context.Background()
	err = st.Write(ctx, func(tx *store.Tx) error {
		if _, err := tx.UserByEmail(ctx, "", key); err == nil {
			return store.ErrConflict
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := tx.InsertUser(ctx, u); err != nil {
			return err
		}
		if err := tx.InsertVerification(ctx, &store.Verification{ID: id.New("vrf"), UserID: u.ID,
			Purpose: store.PurposeVerify, TokenHash: hash, ExpiresAt: expires, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, cliAudit("", "admin.create", u.ID, map[string]any{"email": addr}))
	})
	if errors.Is(err, store.ErrConflict) {
		fmt.Fprintf(stderr, "selfcloud admin create-admin: an admin with email %s exists\n", addr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud admin create-admin:", err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nlink=%s\ntoken=%s\n", u.ID, link, secret)
	fmt.Fprintf(stderr, "The link expires at %s. Send it to the admin; it works once.\n",
		expires.UTC().Format(time.RFC3339))
	return 0
}

// usageDetails is a usage record as the audit log and the command's output show it.
func usageDetails(u store.Usage) map[string]any {
	return map[string]any{"used_bytes": u.Bytes, "used_files": u.Files, "reserved_bytes": u.Reserved}
}

// adminRecountUsage recomputes each account's counters from its files and uploads, one transaction
// per account, and prints the counters before and after. It is the repair for drift; a clamp to zero
// or a sweep warning is the signal to run it.
func adminRecountUsage(args []string, stdout, stderr io.Writer) int {
	var only *string
	st, _, code := adminStore("recount-usage", args, stderr, 0, func(fs *flag.FlagSet) {
		only = fs.String("account", "", "recount only this account (default: every account)")
	})
	if st == nil {
		return code
	}
	defer st.Close()
	ctx := context.Background()
	var accounts []string
	if *only != "" {
		if !id.Valid("acc", *only) {
			fmt.Fprintln(stderr, "selfcloud admin recount-usage: -account is not an account ID")
			return 2
		}
		accounts = []string{*only}
	} else {
		var cursor string
		for {
			page, next, err := st.Accounts(ctx, "", store.Page{Limit: 200, Cursor: cursor})
			if err != nil {
				fmt.Fprintln(stderr, "selfcloud admin recount-usage:", err)
				return 1
			}
			for _, a := range page {
				accounts = append(accounts, a.ID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	for _, acc := range accounts {
		var before, after store.Usage
		err := st.Write(ctx, func(tx *store.Tx) error {
			var err error
			before, after, err = tx.RecountAccount(ctx, acc)
			if err != nil {
				return err
			}
			return tx.InsertAudit(ctx, cliAudit(acc, "account.recount", acc,
				map[string]any{"before": usageDetails(before), "after": usageDetails(after)}))
		})
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintf(stderr, "selfcloud admin recount-usage: no account %s\n", acc)
			return 1
		}
		if err != nil {
			fmt.Fprintln(stderr, "selfcloud admin recount-usage:", err)
			return 1
		}
		fmt.Fprintf(stdout, "account=%s used_bytes=%d->%d used_files=%d->%d reserved_bytes=%d->%d\n",
			acc, before.Bytes, after.Bytes, before.Files, after.Files, before.Reserved, after.Reserved)
	}
	return 0
}
