// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// appPasswordRow adds one app password of user u and returns it.
func appPasswordRow(t *testing.T, s *Store, accountID, userID, access string) *AppPassword {
	t.Helper()
	a := &AppPassword{ID: id.New("apw"), AccountID: accountID, UserID: userID, Name: "mount",
		Access: access, Prefix: "scp_", SecretHash: []byte("hash-" + id.New("x")), CreatedAt: Now()}
	if err := s.Write(context.Background(), func(tx *Tx) error { return tx.InsertAppPassword(context.Background(), a) }); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAppPasswordAccessCheck(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	u := newUserRow(t, s, acc, "ann@example.com", "")

	// read and write are the only access levels the table holds.
	appPasswordRow(t, s, acc, u.ID, AppAccessRead)
	appPasswordRow(t, s, acc, u.ID, AppAccessWrite)
	// Any other value is refused by the column's CHECK, not stored.
	bad := &AppPassword{ID: id.New("apw"), AccountID: acc, UserID: u.ID, Name: "m",
		Access: "admin", Prefix: "scp_", SecretHash: []byte("h"), CreatedAt: Now()}
	err := s.Write(ctx, func(tx *Tx) error { return tx.InsertAppPassword(ctx, bad) })
	if err == nil {
		t.Error("access 'admin' was stored; want the CHECK to refuse it")
	}
}

func TestAppPasswordUserIndex(t *testing.T) {
	s := openTest(t)
	// The list of one user's app passwords, oldest first, is served by an index on (user_id, created_at).
	rows, err := s.r.QueryContext(context.Background(), "PRAGMA index_info(app_passwords_user)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"user_id", "created_at"}
	if len(cols) != len(want) {
		t.Fatalf("index columns = %v, want %v", cols, want)
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Errorf("index columns = %v, want %v", cols, want)
		}
	}
}

func TestAppPasswordRevokeDeletesRow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	u := newUserRow(t, s, acc, "ann@example.com", "")
	a := appPasswordRow(t, s, acc, u.ID, AppAccessRead)

	// Revoking removes the row outright: the list never grows with ended app passwords.
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAppPassword(ctx, acc, u.ID, a.ID) }); err != nil {
		t.Fatal(err)
	}
	list, err := s.AppPasswords(ctx, acc, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("after revoke the row is still listed: %+v", list)
	}
	// Revoking one that is gone is not found.
	err = s.Write(ctx, func(tx *Tx) error { return tx.DeleteAppPassword(ctx, acc, u.ID, a.ID) })
	if err != ErrNotFound {
		t.Errorf("revoking a gone row = %v, want ErrNotFound", err)
	}
}
