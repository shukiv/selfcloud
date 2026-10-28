package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewKey(t *testing.T) {
	secret, display, hash := NewKey(AccountKeyPrefix)
	if !ValidKeyFormat(AccountKeyPrefix, secret) {
		t.Fatalf("NewKey made %q, which ValidKeyFormat rejects", secret)
	}
	if ValidKeyFormat(AdminKeyPrefix, secret) {
		t.Error("an account key passed as an admin key")
	}
	if !strings.HasPrefix(secret, display) || len(display) != len(AccountKeyPrefix)+displayLen {
		t.Errorf("display %q is not a short prefix of the key", display)
	}
	if !bytes.Equal(hash, HashKey(secret)) {
		t.Error("hash does not match HashKey(secret)")
	}
	other, _, _ := NewKey(AccountKeyPrefix)
	if other == secret {
		t.Error("two keys are equal")
	}
}

func TestManagesAccount(t *testing.T) {
	admin := &Principal{Kind: KindAdminKey, ID: "key_a"}
	acct := &Principal{Kind: KindAccountKey, ID: "key_b", AccountID: "acc_1"}
	var none *Principal
	for _, tc := range []struct {
		p    *Principal
		acc  string
		want bool
	}{
		{admin, "acc_1", true},
		{acct, "acc_1", true},
		{acct, "acc_2", false},
		{none, "acc_1", false},
		{&Principal{Kind: KindSystem}, "acc_1", false},
	} {
		if got := tc.p.ManagesAccount(tc.acc); got != tc.want {
			t.Errorf("%+v.ManagesAccount(%s) = %v", tc.p, tc.acc, got)
		}
	}
	if !admin.IsAdmin() || acct.IsAdmin() || none.IsAdmin() {
		t.Error("IsAdmin is wrong")
	}
}
