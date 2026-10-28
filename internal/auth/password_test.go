package auth

import (
	"context"
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	ctx := context.Background()
	h, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") || strings.Count(h, "$") != 5 {
		t.Fatalf("hash %q is not the RFC 9106 PHC form", h)
	}
	if ok, err := VerifyPassword(ctx, h, "correct horse"); err != nil || !ok {
		t.Fatalf("right password: %v, %v", ok, err)
	}
	if ok, err := VerifyPassword(ctx, h, "correct horsf"); err != nil || ok {
		t.Fatalf("wrong password: %v, %v", ok, err)
	}
	// Two hashes of one password differ, because each has its own salt.
	h2, _ := HashPassword(ctx, "correct horse")
	if h == h2 {
		t.Error("two hashes of one password are equal")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	ctx := context.Background()
	for _, h := range []string{
		"",
		"plain text",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=999999999,t=3,p=4$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=0,p=4$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5",
	} {
		if _, err := VerifyPassword(ctx, h, "x"); err == nil {
			t.Errorf("hash %q was accepted", h)
		}
	}
}

func TestDummyVerifyNeverMatches(t *testing.T) {
	if err := DummyVerify(context.Background(), "anything at all"); err != nil {
		t.Fatal(err)
	}
}

func TestValidPassword(t *testing.T) {
	for p, ok := range map[string]bool{
		"1234567":                false,
		"12345678":               true,
		strings.Repeat("é", 8):   true,
		strings.Repeat("a", 256): true,
		strings.Repeat("a", 257): false,
	} {
		if err := ValidPassword(p); (err == nil) != ok {
			t.Errorf("ValidPassword(%d chars) = %v, want ok=%v", len(p), err, ok)
		}
	}
}

func TestNewTokenShapeAndHash(t *testing.T) {
	a, ha := NewToken()
	b, _ := NewToken()
	if a == b || !ValidToken(a) || len(ha) != 32 || string(HashToken(a)) != string(ha) {
		t.Fatalf("tokens %q %q", a, b)
	}
	for _, bad := range []string{"", "short", strings.Repeat("!", 43), strings.Repeat("a", 44)} {
		if ValidToken(bad) {
			t.Errorf("ValidToken(%q) = true", bad)
		}
	}
}

func TestUserPrincipalManagesOwnAccountOnly(t *testing.T) {
	owner := &Principal{Kind: KindUser, ID: "usr_1", AccountID: "acc_1", UserID: "usr_1", Role: RoleOwner}
	member := &Principal{Kind: KindUser, ID: "usr_2", AccountID: "acc_1", UserID: "usr_2", Role: "member"}
	if !owner.ManagesAccount("acc_1") || owner.ManagesAccount("acc_2") {
		t.Error("owner must manage its own account only")
	}
	if member.ManagesAccount("acc_1") {
		t.Error("member must not manage the account")
	}
}
