// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	netmail "net/mail"
	"os"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/recovery"
	"github.com/shukiv/selfcloud/internal/store"
)

// adminStdin is what the local recovery commands read a password line or a confirmation from. Tests replace it.
var adminStdin io.Reader = os.Stdin

const (
	methodLinkRecovery      = "link"
	methodTemporaryRecovery = "temporary"
	// tempPasswordLen is the length of a generated temporary password (owner's answer 5).
	tempPasswordLen = 24
	// tempAlphabet leaves out the characters that look alike (0, O, 1, l, I, o).
	tempAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// maxStdinLine bounds the password line read from stdin, so a runaway input cannot fill memory.
	maxStdinLine   = 4096
	minRecoveryTTL = 5 * time.Minute
	maxRecoveryTTL = 168 * time.Hour
)

// parseRecoveryTTL reads -ttl. An empty value means the default; any other value must be a duration from 5m to 168h.
func parseRecoveryTTL(v string, def time.Duration) (time.Duration, error) {
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("-ttl %q is not a duration", v)
	}
	if d < minRecoveryTTL || d > maxRecoveryTTL {
		return 0, fmt.Errorf("-ttl must be from %s to %s", minRecoveryTTL, maxRecoveryTTL)
	}
	return d, nil
}

// parseBareEmail accepts only a bare address such as name@example.com, as create-admin does.
func parseBareEmail(s string) bool {
	parsed, err := netmail.ParseAddress(s)
	return err == nil && parsed.Address == s
}

// validUserRef reports whether ref is a user ID or a bare email address.
func validUserRef(ref string) bool {
	return id.Valid("usr", ref) || parseBareEmail(ref)
}

// resolveUserRef turns -user into a user ID. An ID is used as it is; an email must belong to exactly one user. A
// matching email of several users is refused with exit 2, and the IDs and accounts are listed on stderr.
func resolveUserRef(ctx context.Context, st *store.Store, ref string, stderr io.Writer, name string) (string, int) {
	if id.Valid("usr", ref) {
		return ref, 0
	}
	rows, err := st.FindUsersByEmail(ctx, strings.ToLower(ref), "", 10)
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return "", 1
	}
	switch len(rows) {
	case 0:
		fmt.Fprintf(stderr, "selfcloud admin %s: no user has the email %s\n", name, ref)
		return "", 1
	case 1:
		return rows[0].ID, 0
	}
	fmt.Fprintf(stderr, "selfcloud admin %s: %s belongs to %d users; pass the user ID instead:\n", name, ref, len(rows))
	for _, u := range rows {
		acc := u.AccountID
		if acc == "" {
			acc = "- (installation admin)"
		}
		fmt.Fprintf(stderr, "  %s\taccount=%s\n", u.ID, acc)
	}
	return "", 2
}

// loadUser reads the user with the given ID, in any account.
func loadUser(ctx context.Context, st *store.Store, userID string) (*store.User, error) {
	var u *store.User
	err := st.Write(ctx, func(tx *store.Tx) error {
		var err error
		u, err = tx.UserByID(ctx, userID)
		return err
	})
	return u, err
}

// checkRecoveryState refuses a user the recovery may not reset: a pending user has no password to reset, and a
// disabled one needs -enable, so a reset never re-enables someone quietly.
func checkRecoveryState(u *store.User, enable bool) error {
	switch u.Status {
	case store.UserPending:
		return fmt.Errorf("%s is pending; verify it first with `selfcloud admin verify`", u.ID)
	case store.UserDisabled:
		if !enable {
			return fmt.Errorf("%s is disabled; pass -enable to enable it with the reset", u.ID)
		}
		if u.VerifiedAt == nil {
			return neverVerifiedErr(u.ID)
		}
	}
	return nil
}

// neverVerifiedErr refuses to enable a user who never verified its email. Requirement 4 holds: every user is verified
// before it signs in, so enabling must not skip the verification.
func neverVerifiedErr(userID string) error {
	return fmt.Errorf("%s has never verified its email; verify it first with `selfcloud admin verify -enable`", userID)
}

// disableGuards refuses to disable the last active installation admin, or the last active owner of an account, as the
// API's disable does. It runs before the prompt, so a refusal is never preceded by a request for a confirmation, and again
// inside the write.
func disableGuards(ctx context.Context, tx *store.Tx, cur *store.User) error {
	if cur.AccountID == "" && cur.Role == store.RoleAdmin {
		others, err := tx.CountActiveAdmins(ctx, cur.ID)
		if err != nil {
			return err
		}
		if others == 0 {
			return fmt.Errorf("%s is the last active installation admin and cannot be disabled", cur.ID)
		}
	}
	if cur.AccountID != "" && cur.Role == store.RoleOwner {
		others, err := tx.CountActiveOwners(ctx, cur.AccountID, cur.ID)
		if err != nil {
			return err
		}
		if others == 0 {
			return fmt.Errorf("%s is the last active owner of its account and cannot be disabled", cur.ID)
		}
	}
	return nil
}

// confirmed reports whether a destructive command may go ahead. -yes confirms it. At a terminal the person types the
// user's email. Anything else is refused, so a script must say -yes.
func confirmed(yes bool, want string, stderr io.Writer) bool {
	if yes {
		return true
	}
	f, ok := adminStdin.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Fprintf(stderr, "Type %s to confirm: ", want)
	line, _ := bufio.NewReader(f).ReadString('\n')
	return strings.TrimSpace(line) == want
}

// readPasswordLine reads one line from r and strips its line ending. The line is not otherwise changed.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, maxStdinLine+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(line) > maxStdinLine {
		return "", errors.New("the password line is too long")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if line == "" {
		return "", errors.New("no password on stdin")
	}
	return line, nil
}

// generatePassword returns n characters from crypto/rand, each drawn from tempAlphabet.
func generatePassword(n int) (string, error) {
	max := big.NewInt(int64(len(tempAlphabet)))
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = tempAlphabet[k.Int64()]
	}
	return string(b), nil
}

// recoveryAudit is the audit entry of a local recovery action. It never holds the token, the link or the password.
func recoveryAudit(accountID, action, userID string, details map[string]any) *store.AuditEntry {
	details["via"] = "cli"
	return cliAudit(accountID, action, userID, details)
}

// adminResetPassword resets a user's password from the data folder. The default is a one-time link. The temporary
// method sets a password, ends the user's sessions and makes the next change mandatory. It needs no server and no
// mail, and it ignores admin.tenant_access, because the operator already holds the data folder.
func adminResetPassword(args []string, stdout, stderr io.Writer) int {
	var user, method, ttlText, linkFile *string
	var enable, passStdin, yes *bool
	cfg, _, code := loadConfig("admin reset-password", args, stderr, 0, func(fs *flag.FlagSet) {
		user = fs.String("user", "", "the user's ID, or an email that exactly one user has (required)")
		method = fs.String("method", methodLinkRecovery, "link (a one-time link, the default) or temporary (a generated password, printed once)")
		enable = fs.Bool("enable", false, "enable a disabled user with the reset")
		ttlText = fs.String("ttl", "", "how long the link works, from 5m to 168h (default users.reset_ttl; link only)")
		linkFile = fs.String("link-file", "", "write the link to this new file (mode 0600) instead of stdout (link only)")
		passStdin = fs.Bool("password-stdin", false, "read the new password from one line of stdin (temporary only)")
		yes = fs.Bool("yes", false, "confirm the temporary method when stdin is not a terminal")
	})
	if cfg == nil {
		return code
	}
	const name = "reset-password"
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s\n", name, msg)
		return 2
	}
	if *user == "" || !validUserRef(*user) {
		return usage("-user must be a user ID or an email address")
	}
	if *method != methodLinkRecovery && *method != methodTemporaryRecovery {
		return usage("-method must be link or temporary")
	}
	isLink := *method == methodLinkRecovery
	if !isLink && (*linkFile != "" || *ttlText != "") {
		return usage("-link-file and -ttl apply to the link method only")
	}
	if isLink && *passStdin {
		return usage("-password-stdin applies to the temporary method only")
	}
	if *linkFile != "" && *passStdin {
		return usage("-link-file and -password-stdin cannot be used together")
	}
	ttl, err := parseRecoveryTTL(*ttlText, 0)
	if err != nil {
		return usage(err.Error())
	}

	ctx := context.Background()
	st, c := adminStoreFor(cfg, stderr, name)
	if st == nil {
		return c
	}
	defer st.Close()
	userID, code := resolveUserRef(ctx, st, *user, stderr, name)
	if code != 0 {
		return code
	}
	u, err := loadUser(ctx, st, userID)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "selfcloud admin %s: no user %s\n", name, userID)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	if err := checkRecoveryState(u, *enable); err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	if isLink {
		defTTL, tmpl := recovery.DefaultLifetime(cfg.Users, store.PurposeReset)
		if ttl == 0 {
			ttl = defTTL
		}
		return resetWithLink(ctx, st, cfg, u, *enable, ttl, tmpl, *linkFile, stdout, stderr)
	}
	return resetWithTemporary(ctx, st, u, *enable, *passStdin, *yes, stdout, stderr)
}

// adminStoreFor opens the store for a recovery command, printing the failure.
func adminStoreFor(cfg *config.Config, stderr io.Writer, name string) (*store.Store, int) {
	log := newLogger(config.LogConfig{Level: "warn", Format: cfg.Log.Format}, stderr)
	st, err := openStore(context.Background(), cfg, log)
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return nil, 1
	}
	return st, 0
}

// resetWithLink issues a reset link. The earlier reset links are voided by recovery.Issue. With -link-file the file is
// created first, and removed again if the change does not commit, so a refused run leaves nothing behind.
func resetWithLink(ctx context.Context, st *store.Store, cfg *config.Config, u *store.User, enable bool, ttl time.Duration,
	tmpl string, linkFile string, stdout, stderr io.Writer) int {
	const name = "reset-password"
	var (
		f       *os.File
		tok     *recovery.Token
		enabled bool
	)
	if linkFile != "" {
		var err error
		if f, err = openLinkFile(linkFile); err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
			return 1
		}
	}
	err := st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if err := checkRecoveryState(cur, enable); err != nil {
			return err
		}
		now := store.Now()
		if enable && cur.Status == store.UserDisabled {
			cur.Status = store.UserActive
			cur.UpdatedAt = now
			if err := tx.UpdateUser(ctx, cur); err != nil {
				return err
			}
			enabled = true
		}
		tok, err = recovery.Issue(ctx, tx, cur, store.PurposeReset, ttl, tmpl, cfg.PublicURL, now)
		if err != nil {
			return err
		}
		if f != nil {
			if _, err := f.WriteString(tok.Link + "\n"); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.password_reset", cur.ID,
			map[string]any{"method": methodLinkRecovery, "ttl": int64(ttl / time.Second), "enabled": enabled}))
	})
	if f != nil {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}
	if err != nil {
		if linkFile != "" {
			_ = os.Remove(linkFile) // created above by this run, so nothing else is removed
		}
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nmethod=%s\n", u.ID, methodLinkRecovery)
	if linkFile != "" {
		fmt.Fprintf(stdout, "link_file=%s\n", linkFile)
	} else {
		fmt.Fprintf(stdout, "link=%s\n", tok.Link)
	}
	fmt.Fprintf(stdout, "expires=%s\n", tok.Expires.UTC().Format(time.RFC3339))
	return 0
}

// resetWithTemporary sets a temporary password and ends the user's sessions, tokens and impersonations in one write. The
// user must change the password at the next sign-in, and sign-in itself still succeeds. The password is printed once,
// unless it came from stdin.
func resetWithTemporary(ctx context.Context, st *store.Store, u *store.User, enable, passStdin, yes bool, stdout, stderr io.Writer) int {
	const name = "reset-password"
	if !confirmed(yes, u.Email, stderr) {
		fmt.Fprintf(stderr, "selfcloud admin %s: refused: the temporary method ends the user's sessions; pass -yes to "+
			"confirm when stdin is not a terminal\n", name)
		return 2
	}
	pw := ""
	if passStdin {
		line, err := readPasswordLine(adminStdin)
		if err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
			return 2
		}
		pw = line
	} else {
		generated, err := generatePassword(tempPasswordLen)
		if err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
			return 1
		}
		pw = generated
	}
	if err := recovery.CheckPassword(pw); err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 2
	}
	hash, err := auth.HashPassword(ctx, pw)
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	enabled := false
	err = st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if err := checkRecoveryState(cur, enable); err != nil {
			return err
		}
		now := store.Now()
		if enable && cur.Status == store.UserDisabled {
			cur.Status = store.UserActive
			enabled = true
		}
		cur.PasswordHash = hash
		cur.MustChangePassword = true
		cur.UpdatedAt = now
		if err := tx.UpdateUser(ctx, cur); err != nil {
			return err
		}
		if err := tx.DeleteVerifications(ctx, cur.ID, store.PurposeReset, ""); err != nil {
			return err
		}
		if _, err := recovery.EndSessions(ctx, tx, cur, "password_reset"); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.password_reset", cur.ID,
			map[string]any{"method": methodTemporaryRecovery, "enabled": enabled}))
	})
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nmethod=%s\n", u.ID, methodTemporaryRecovery)
	if passStdin {
		fmt.Fprintln(stdout, "password=set")
	} else {
		fmt.Fprintf(stdout, "password=%s\n", pw)
		fmt.Fprintln(stderr, "The temporary password is shown only once: give it to the user now.")
	}
	return 0
}

// adminUsers lists users from the data folder as a tab-separated table: id, email, role, status, account, created and
// must_change. It holds no hash and no secret. Without a filter it lists the installation admins and every account.
func adminUsers(args []string, stdout, stderr io.Writer) int {
	var account, email, status *string
	var admins *bool
	cfg, _, code := loadConfig("admin users", args, stderr, 0, func(fs *flag.FlagSet) {
		account = fs.String("account", "", "list only this account's users (an account ID)")
		admins = fs.Bool("admins", false, "list only the installation admins")
		email = fs.String("email", "", "list only the user with this email address")
		status = fs.String("status", "", "list only users with this status: active, pending or disabled")
	})
	if cfg == nil {
		return code
	}
	const name = "users"
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s\n", name, msg)
		return 2
	}
	if *account != "" && !id.Valid("acc", *account) {
		return usage("-account must be an account ID")
	}
	if *admins && *account != "" {
		return usage("-admins lists the installation admins, so it cannot be combined with -account")
	}
	if *email != "" && !parseBareEmail(*email) {
		return usage("-email must be a bare email address")
	}
	switch *status {
	case "", store.UserActive, store.UserPending, store.UserDisabled:
	default:
		return usage("-status must be active, pending or disabled")
	}

	ctx := context.Background()
	st, c := adminStoreFor(cfg, stderr, name)
	if st == nil {
		return c
	}
	defer st.Close()
	scopes := []string{""}
	switch {
	case *admins:
	case *account != "":
		scopes = []string{*account}
	default:
		for cursor := ""; ; {
			accs, next, err := st.Accounts(ctx, "", store.Page{Limit: 200, Cursor: cursor})
			if err != nil {
				fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
				return 1
			}
			for _, a := range accs {
				scopes = append(scopes, a.ID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	filter := store.UserFilter{}
	if *email != "" {
		filter.EmailKey = strings.ToLower(*email)
	}
	fmt.Fprintln(stdout, "id\temail\trole\tstatus\taccount\tcreated\tmust_change")
	for _, scope := range scopes {
		for cursor := ""; ; {
			users, next, err := st.Users(ctx, scope, filter, store.Page{Limit: 200, Cursor: cursor})
			if err != nil {
				fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
				return 1
			}
			for _, u := range users {
				if *status != "" && u.Status != *status {
					continue
				}
				acc := u.AccountID
				if acc == "" {
					acc = "-"
				}
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%t\n", u.ID, u.Email, u.Role, u.Status, acc,
					u.CreatedAt.UTC().Format(time.RFC3339), u.MustChangePassword)
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
	return 0
}

// userStateCommand loads the user that -user names, for the commands that change a user's status. It prints and returns
// the exit code when the user cannot be found.
func userStateCommand(ctx context.Context, cfg *config.Config, ref, name string, stderr io.Writer) (*store.Store, *store.User, int) {
	st, c := adminStoreFor(cfg, stderr, name)
	if st == nil {
		return nil, nil, c
	}
	userID, code := resolveUserRef(ctx, st, ref, stderr, name)
	if code != 0 {
		st.Close()
		return nil, nil, code
	}
	u, err := loadUser(ctx, st, userID)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "selfcloud admin %s: no user %s\n", name, userID)
		st.Close()
		return nil, nil, 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		st.Close()
		return nil, nil, 1
	}
	return st, u, 0
}

// adminEnable sets a disabled user back to active. A pending user is verified first, and an active user is already
// enabled. The change is audited.
func adminEnable(args []string, stdout, stderr io.Writer) int {
	var user *string
	cfg, _, code := loadConfig("admin enable", args, stderr, 0, func(fs *flag.FlagSet) {
		user = fs.String("user", "", "the user's ID, or an email that exactly one user has (required)")
	})
	if cfg == nil {
		return code
	}
	const name = "enable"
	if *user == "" || !validUserRef(*user) {
		fmt.Fprintf(stderr, "selfcloud admin %s: -user must be a user ID or an email address\n", name)
		return 2
	}
	ctx := context.Background()
	st, u, code := userStateCommand(ctx, cfg, *user, name, stderr)
	if st == nil {
		return code
	}
	defer st.Close()
	if u.Status != store.UserDisabled {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s is %s; only a disabled user is enabled\n", name, u.ID, u.Status)
		return 1
	}
	err := st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Status != store.UserDisabled {
			return fmt.Errorf("%s is %s; only a disabled user is enabled", cur.ID, cur.Status)
		}
		if cur.VerifiedAt == nil {
			return neverVerifiedErr(cur.ID)
		}
		cur.Status = store.UserActive
		cur.UpdatedAt = store.Now()
		if err := tx.UpdateUser(ctx, cur); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.enable", cur.ID, map[string]any{}))
	})
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nstatus=%s\n", u.ID, store.UserActive)
	return 0
}

// adminDisable disables an active user and ends its sessions, tokens and impersonations, as the API's disable does. It
// refuses the last active installation admin, so the installation keeps one that can sign in. It asks for -yes, or for
// the user's email at a terminal, because it ends the user's sessions.
func adminDisable(args []string, stdout, stderr io.Writer) int {
	var user *string
	var yes *bool
	cfg, _, code := loadConfig("admin disable", args, stderr, 0, func(fs *flag.FlagSet) {
		user = fs.String("user", "", "the user's ID, or an email that exactly one user has (required)")
		yes = fs.Bool("yes", false, "confirm the disable when stdin is not a terminal")
	})
	if cfg == nil {
		return code
	}
	const name = "disable"
	if *user == "" || !validUserRef(*user) {
		fmt.Fprintf(stderr, "selfcloud admin %s: -user must be a user ID or an email address\n", name)
		return 2
	}
	ctx := context.Background()
	st, u, code := userStateCommand(ctx, cfg, *user, name, stderr)
	if st == nil {
		return code
	}
	defer st.Close()
	if u.Status != store.UserActive {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s is %s; only an active user is disabled\n", name, u.ID, u.Status)
		return 1
	}
	if err := st.Write(ctx, func(tx *store.Tx) error { return disableGuards(ctx, tx, u) }); err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	if !confirmed(*yes, u.Email, stderr) {
		fmt.Fprintf(stderr, "selfcloud admin %s: refused: the disable ends the user's sessions; pass -yes to confirm "+
			"when stdin is not a terminal\n", name)
		return 2
	}
	err := st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if cur.Status != store.UserActive {
			return fmt.Errorf("%s is %s; only an active user is disabled", cur.ID, cur.Status)
		}
		if err := disableGuards(ctx, tx, cur); err != nil {
			return err
		}
		cur.Status = store.UserDisabled
		cur.UpdatedAt = store.Now()
		if err := tx.UpdateUser(ctx, cur); err != nil {
			return err
		}
		if _, err := recovery.EndSessions(ctx, tx, cur, "disabled"); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.disable", cur.ID, map[string]any{}))
	})
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nstatus=%s\n", u.ID, store.UserDisabled)
	return 0
}

// adminEndSessions ends a user's sessions, delegated tokens and impersonations now, without changing the password or
// the status. It asks for -yes, or for the user's email at a terminal.
func adminEndSessions(args []string, stdout, stderr io.Writer) int {
	var user *string
	var yes *bool
	cfg, _, code := loadConfig("admin end-sessions", args, stderr, 0, func(fs *flag.FlagSet) {
		user = fs.String("user", "", "the user's ID, or an email that exactly one user has (required)")
		yes = fs.Bool("yes", false, "confirm the end when stdin is not a terminal")
	})
	if cfg == nil {
		return code
	}
	const name = "end-sessions"
	if *user == "" || !validUserRef(*user) {
		fmt.Fprintf(stderr, "selfcloud admin %s: -user must be a user ID or an email address\n", name)
		return 2
	}
	ctx := context.Background()
	st, u, code := userStateCommand(ctx, cfg, *user, name, stderr)
	if st == nil {
		return code
	}
	defer st.Close()
	if !confirmed(*yes, u.Email, stderr) {
		fmt.Fprintf(stderr, "selfcloud admin %s: refused: this ends the user's sessions; pass -yes to confirm "+
			"when stdin is not a terminal\n", name)
		return 2
	}
	err := st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if _, err := recovery.EndSessions(ctx, tx, cur, "sessions_ended"); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.sessions_ended", cur.ID, map[string]any{}))
	})
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\nsessions=ended\n", u.ID)
	return 0
}

// openLinkFile creates a new file for a link, with mode 0600. It refuses a file that is already there, so an earlier
// link is never overwritten.
func openLinkFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%s exists; refusing to overwrite it", path)
	}
	return f, err
}

// checkVerifyState refuses a user that a verify link may not be issued for. An active user is already verified. A user
// who was verified and is disabled goes back through admin enable. A user who never verified and is disabled needs
// -enable, and then it goes back to pending, never active: the verify route activates it.
func checkVerifyState(u *store.User, enable bool) error {
	switch u.Status {
	case store.UserActive:
		return fmt.Errorf("%s is already verified", u.ID)
	case store.UserDisabled:
		if u.VerifiedAt != nil {
			return fmt.Errorf("%s is disabled; use `selfcloud admin enable`", u.ID)
		}
		if !enable {
			return fmt.Errorf("%s is disabled and never verified; pass -enable to set it back to pending and issue the link", u.ID)
		}
	}
	return nil
}

// adminVerify issues a fresh verification link for a pending user, as create-admin does, and voids the earlier one. It
// covers an admin whose create-admin link was lost. The link is printed, or written to -link-file.
func adminVerify(args []string, stdout, stderr io.Writer) int {
	var user, ttlText, linkFile *string
	var enable *bool
	cfg, _, code := loadConfig("admin verify", args, stderr, 0, func(fs *flag.FlagSet) {
		user = fs.String("user", "", "the user's ID, or an email that exactly one user has (required)")
		ttlText = fs.String("ttl", "", "how long the link works, from 5m to 168h (default users.verification_ttl)")
		linkFile = fs.String("link-file", "", "write the link to this new file (mode 0600) instead of stdout")
		enable = fs.Bool("enable", false, "for a disabled user who never verified: set it back to pending and issue the link")
	})
	if cfg == nil {
		return code
	}
	const name = "verify"
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "selfcloud admin %s: %s\n", name, msg)
		return 2
	}
	if *user == "" || !validUserRef(*user) {
		return usage("-user must be a user ID or an email address")
	}
	ttl, err := parseRecoveryTTL(*ttlText, 0)
	if err != nil {
		return usage(err.Error())
	}
	ctx := context.Background()
	st, c := adminStoreFor(cfg, stderr, name)
	if st == nil {
		return c
	}
	defer st.Close()
	userID, code := resolveUserRef(ctx, st, *user, stderr, name)
	if code != 0 {
		return code
	}
	u, err := loadUser(ctx, st, userID)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "selfcloud admin %s: no user %s\n", name, userID)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	if err := checkVerifyState(u, *enable); err != nil {
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	defTTL, tmpl := recovery.DefaultLifetime(cfg.Users, store.PurposeVerify)
	if ttl == 0 {
		ttl = defTTL
	}

	var f *os.File
	if *linkFile != "" {
		if f, err = openLinkFile(*linkFile); err != nil {
			fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
			return 1
		}
	}
	var tok *recovery.Token
	err = st.Write(ctx, func(tx *store.Tx) error {
		cur, err := tx.UserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if err := checkVerifyState(cur, *enable); err != nil {
			return err
		}
		fromDisabled := cur.Status == store.UserDisabled
		now := store.Now()
		if fromDisabled {
			// Back to pending, never active: the verify route makes it active once the link is used.
			cur.Status = store.UserPending
			cur.UpdatedAt = now
			if err := tx.UpdateUser(ctx, cur); err != nil {
				return err
			}
		}
		tok, err = recovery.Issue(ctx, tx, cur, store.PurposeVerify, ttl, tmpl, cfg.PublicURL, now)
		if err != nil {
			return err
		}
		if f != nil {
			if _, err := f.WriteString(tok.Link + "\n"); err != nil {
				return err
			}
		}
		return tx.InsertAudit(ctx, recoveryAudit(cur.AccountID, "user.verification_issued", cur.ID,
			map[string]any{"purpose": store.PurposeVerify, "ttl": int64(ttl / time.Second), "from_disabled": fromDisabled}))
	})
	if f != nil {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}
	if err != nil {
		if *linkFile != "" {
			_ = os.Remove(*linkFile) // created above by this run, so nothing else is removed
		}
		fmt.Fprintf(stderr, "selfcloud admin %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "user_id=%s\n", u.ID)
	if *linkFile != "" {
		fmt.Fprintf(stdout, "link_file=%s\n", *linkFile)
	} else {
		fmt.Fprintf(stdout, "link=%s\n", tok.Link)
	}
	fmt.Fprintf(stdout, "expires=%s\n", tok.Expires.UTC().Format(time.RFC3339))
	return 0
}
