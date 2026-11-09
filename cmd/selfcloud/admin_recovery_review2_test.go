// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/recovery"
	"github.com/shukiv/selfcloud/internal/store"
)

// withBeforeWriteHook sets testHookBeforeWrite for the rest of the test.
func withBeforeWriteHook(t *testing.T, h func(st *store.Store, userID string)) {
	t.Helper()
	testHookBeforeWrite = h
	t.Cleanup(func() { testHookBeforeWrite = nil })
}

// auditFor reports whether an audit row with the action names the target.
func (f *recoveryFixture) auditFor(accountID, action, target string) bool {
	f.t.Helper()
	for _, row := range f.auditRows(accountID) {
		if row.Action == action && row.Target == target {
			return true
		}
	}
	return false
}

// TestDisableRechecksTheAdminGuardInsideTheWrite: the last active installation admin is refused when the other admin is
// disabled between the pre-check and the write. Nothing is written.
func TestDisableRechecksTheAdminGuardInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	a := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	b := f.seedUser("", "second@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == a.ID {
			f.setStatus(b, store.UserDisabled)
		}
	})
	code, _, errOut := runCmd(t, "admin", "disable", "-user", a.ID, "-yes")
	if code != 1 || !strings.Contains(errOut, "last") {
		t.Fatalf("disable with the other admin gone = %d %q; want 1 naming the last admin", code, errOut)
	}
	if f.reload(a).Status != store.UserActive || f.auditFor("", "user.disable", a.ID) {
		t.Fatal("the refused disable wrote something")
	}
}

// TestDisableRechecksTheOwnerGuardInsideTheWrite: the same for the last active owner of an account.
func TestDisableRechecksTheOwnerGuardInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	a := f.seedUser(acc.ID, "owner@example.com", store.RoleOwner, store.UserActive, "old passphrase")
	b := f.seedUser(acc.ID, "second@example.com", store.RoleOwner, store.UserActive, "old passphrase")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == a.ID {
			f.setStatus(b, store.UserDisabled)
		}
	})
	code, _, errOut := runCmd(t, "admin", "disable", "-user", a.ID, "-yes")
	if code != 1 || !strings.Contains(errOut, "owner") {
		t.Fatalf("disable with the other owner gone = %d %q; want 1 naming the owner", code, errOut)
	}
	if f.reload(a).Status != store.UserActive || f.auditFor(acc.ID, "user.disable", a.ID) {
		t.Fatal("the refused disable wrote something")
	}
}

// TestResetLinkRechecksInsideTheWrite: a link is not issued when the user became pending, or became never-verified
// with -enable, between the pre-check and the write.
func TestResetLinkRechecksInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == u.ID {
			f.setStatus(u, store.UserPending)
		}
	})
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID)
	if code != 1 || !strings.Contains(errOut, "pending") || strings.Contains(out, "link=") {
		t.Fatalf("reset of a user that became pending = %d %q %q; want 1 naming pending, no link", code, out, errOut)
	}
	if f.reload(u).Status != store.UserPending || f.auditFor(acc.ID, "user.password_reset", u.ID) {
		t.Fatal("the refused reset wrote something")
	}

	d := f.seedUser(acc.ID, "old@example.com", store.RoleMember, store.UserDisabled, "old passphrase")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == d.ID {
			f.clearVerified(d)
		}
	})
	code, out, errOut = runCmd(t, "admin", "reset-password", "-user", d.ID, "-enable")
	if code != 1 || !strings.Contains(errOut, "never verified") || strings.Contains(out, "link=") {
		t.Fatalf("reset -enable of a user that became never-verified = %d %q %q; want 1", code, out, errOut)
	}
	if f.reload(d).Status != store.UserDisabled || f.auditFor(acc.ID, "user.password_reset", d.ID) {
		t.Fatal("the refused reset -enable wrote something")
	}
}

// TestResetTemporaryRechecksInsideTheWrite: a temporary password is not set when the user became pending between the
// pre-check and the write. The old password still signs in and nothing must change.
func TestResetTemporaryRechecksInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserActive, "old passphrase")
	ts, c := f.serve()
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == u.ID {
			f.setStatus(u, store.UserPending)
		}
	})
	code, out, errOut := runCmd(t, "admin", "reset-password", "-user", u.ID, "-method", "temporary", "-yes")
	if code != 1 || !strings.Contains(errOut, "pending") || strings.Contains(out, "password=") {
		t.Fatalf("temporary reset of a user that became pending = %d %q %q; want 1 naming pending", code, out, errOut)
	}
	got := f.reload(u)
	if got.MustChangePassword || f.auditFor(acc.ID, "user.password_reset", u.ID) {
		t.Fatal("the refused temporary reset wrote something")
	}
	f.setStatus(u, store.UserActive)
	if code, _ := login(t, ts, c, u.Email, "old passphrase"); code != 200 {
		t.Errorf("the old password no longer signs in after a refused reset: %d", code)
	}
}

// TestEnableRechecksInsideTheWrite: enable refuses a user who became never-verified between the pre-check and the write.
func TestEnableRechecksInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserDisabled, "old passphrase")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == u.ID {
			f.clearVerified(u)
		}
	})
	code, _, errOut := runCmd(t, "admin", "enable", "-user", u.ID)
	if code != 1 || !strings.Contains(errOut, "never verified") {
		t.Fatalf("enable of a user that became never-verified = %d %q; want 1", code, errOut)
	}
	if f.reload(u).Status != store.UserDisabled || f.auditFor(acc.ID, "user.enable", u.ID) {
		t.Fatal("the refused enable wrote something")
	}
}

// TestVerifyRechecksInsideTheWrite: a verify link is not issued when the user became active between the pre-check and the
// write.
func TestVerifyRechecksInsideTheWrite(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserPending, "")
	withBeforeWriteHook(t, func(_ *store.Store, id string) {
		if id == u.ID {
			f.setStatus(u, store.UserActive)
		}
	})
	code, out, errOut := runCmd(t, "admin", "verify", "-user", u.ID)
	if code != 1 || !strings.Contains(errOut, "already verified") || strings.Contains(out, "link=") {
		t.Fatalf("verify of a user that became active = %d %q %q; want 1 naming already verified", code, out, errOut)
	}
	if f.auditFor(acc.ID, "user.verification_issued", u.ID) {
		t.Fatal("the refused verify wrote an audit row")
	}
}

// TestResetLinkCleansUpAfterAFailedCommit: when the write fails after the link file is created, the file is removed. The
// store is closed, so the write fails with a real error.
func TestResetLinkCleansUpAfterAFailedCommit(t *testing.T) {
	f := newRecoveryFixture(t)
	u := f.seedUser("", "root@example.com", store.RoleAdmin, store.UserActive, "old passphrase")
	path := filepath.Join(t.TempDir(), "link.txt")
	ttl, tmpl := recovery.DefaultLifetime(f.cfg.Users, store.PurposeReset)
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := resetWithLink(f.ctx, f.st, f.cfg, u, false, ttl, tmpl, path, &out, &errOut); code != 1 {
		t.Fatalf("resetWithLink on a closed store = %d %q, want 1", code, errOut.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the link file survived a failed commit: %v", err)
	}
	if strings.Contains(out.String(), "link=") {
		t.Fatal("a failed reset printed a link")
	}
}

// TestVerifyCleansUpAfterAFailedCommit: the same for verify. The hook closes the store between the pre-check and the
// write, so the write fails after the file is created.
func TestVerifyCleansUpAfterAFailedCommit(t *testing.T) {
	f := newRecoveryFixture(t)
	acc := f.seedAccount("Acme")
	u := f.seedUser(acc.ID, "ann@example.com", store.RoleMember, store.UserPending, "")
	path := filepath.Join(t.TempDir(), "link.txt")
	withBeforeWriteHook(t, func(st *store.Store, _ string) { st.Close() })
	code, out, errOut := runCmd(t, "admin", "verify", "-user", u.ID, "-link-file", path)
	if code != 1 || strings.Contains(out, "link_file=") {
		t.Fatalf("verify over a failed commit = %d %q %q; want 1", code, out, errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the link file survived a failed commit: %v", err)
	}
}
