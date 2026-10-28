package id

import "testing"

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		s := New("acc")
		if !Valid("acc", s) {
			t.Fatalf("New returned %q, which Valid rejects", s)
		}
		if seen[s] {
			t.Fatalf("duplicate ID %q", s)
		}
		seen[s] = true
	}
}

func TestValid(t *testing.T) {
	good := New("usr")
	for _, tc := range []struct {
		prefix, s string
		want      bool
	}{
		{"usr", good, true},
		{"acc", good, false},
		{"usr", "usr_", false},
		{"usr", good + "0", false},
		{"usr", "usr_ABCDEFGHJKMNPQRSTVWXYZ01", false}, // uppercase is not in the alphabet
		{"usr", "usr_iiiiiiiiiiiiiiiiiiiiiiiiii", false},
	} {
		if got := Valid(tc.prefix, tc.s); got != tc.want {
			t.Errorf("Valid(%q, %q) = %v, want %v", tc.prefix, tc.s, got, tc.want)
		}
	}
}
