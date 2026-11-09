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
		f, err = os.OpenFile(linkFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintf(stderr, "selfcloud admin %s: %s exists; refusing to overwrite it\n", name, linkFile)
			return 1
		}
		if err != nil {
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
