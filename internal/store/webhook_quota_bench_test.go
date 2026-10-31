// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// benchUsage is an account with one team folder and endpoints active endpoints subscribed to events. It is the
// fixture of the usage benchmarks: AdjustUsage is called on the folder, in a transaction, as an upload's write does.
func benchUsage(b *testing.B, events []string, endpoints int) (*Store, string, string) {
	b.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(b.TempDir(), "usage.db")})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	now := Now()
	acc := &Account{ID: id.New("acc"), Name: "Bench", Status: AccountActive, CreatedAt: now, UpdatedAt: now}
	f := &Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Team", NameKey: "team", CreatedAt: now, UpdatedAt: now}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		return tx.InsertFolder(ctx, f, "usr_bench")
	}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < endpoints; i++ {
		ep := &WebhookEndpoint{ID: id.New("whk"), AccountID: acc.ID, URL: fmt.Sprintf("https://hook%d.example.com/in", i),
			Events: events, State: WebhookActive, SecretEnc: []byte("sealed"), CreatedBy: "key_bench",
			CreatedAt: now, UpdatedAt: now}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertWebhookEndpoint(ctx, ep) }); err != nil {
			b.Fatal(err)
		}
	}
	return s, acc.ID, f.ID
}

// benchUsageWrites runs one upload-sized usage change per transaction, the way a handler's write does.
func benchUsageWrites(b *testing.B, s *Store, accountID, folderID string) {
	b.Helper()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.AdjustUsage(ctx, accountID, folderID, UsageChange{Bytes: 5, Files: 1})
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// benchUsageCalls runs b.N usage changes in one transaction, so the cost of a call is not hidden by the commit.
func benchUsageCalls(b *testing.B, s *Store, accountID, folderID string) {
	b.Helper()
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < b.N; i++ {
			if err := tx.AdjustUsage(ctx, accountID, folderID, UsageChange{Bytes: 5, Files: 1}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkUsageWriteNoEndpoints is one usage change in its own transaction, in an account with no endpoint.
func BenchmarkUsageWriteNoEndpoints(b *testing.B) {
	s, acc, f := benchUsage(b, nil, 0)
	benchUsageWrites(b, s, acc, f)
}

// BenchmarkUsageWriteFileEndpoint is the same change in an account whose one endpoint subscribes to a file event only.
func BenchmarkUsageWriteFileEndpoint(b *testing.B) {
	s, acc, f := benchUsage(b, []string{"file.created"}, 1)
	benchUsageWrites(b, s, acc, f)
}

// BenchmarkUsageWriteQuotaEndpoint is the same change in an account whose one endpoint subscribes to quota.warning.
func BenchmarkUsageWriteQuotaEndpoint(b *testing.B) {
	s, acc, f := benchUsage(b, []string{"quota.warning"}, 1)
	benchUsageWrites(b, s, acc, f)
}

// BenchmarkUsageCallsNoEndpoints is the cost of one usage change inside a transaction, with no endpoint.
func BenchmarkUsageCallsNoEndpoints(b *testing.B) {
	s, acc, f := benchUsage(b, nil, 0)
	benchUsageCalls(b, s, acc, f)
}

// BenchmarkUsageCallsFileEndpoint is the same inside a transaction, with an endpoint that subscribes to a file event.
func BenchmarkUsageCallsFileEndpoint(b *testing.B) {
	s, acc, f := benchUsage(b, []string{"file.created"}, 1)
	benchUsageCalls(b, s, acc, f)
}

// BenchmarkUsageCallsQuotaEndpoint is the same inside a transaction, with an endpoint that subscribes to quota.warning.
func BenchmarkUsageCallsQuotaEndpoint(b *testing.B) {
	s, acc, f := benchUsage(b, []string{"quota.warning"}, 1)
	benchUsageCalls(b, s, acc, f)
}
