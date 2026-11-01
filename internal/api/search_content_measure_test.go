// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureContentCosts records what content search costs once it runs (slice q3, decision 46): the worker's time per
// file, the database's growth for a 1 MiB text, and the latency of a content search. It runs only when
// SELFCLOUD_MEASURE_CONTENT=1, and logs what it measured; the figures are in decision 46. The backfill's cost is measured
// in the store package (TestMeasureBackfill), where the rows can be written in bulk.
func TestMeasureContentCosts(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure content search")
	}
	fx := newContentFixture(t)
	const files = 2000
	for i := 0; i < files; i++ {
		fx.file(t, "f"+strconv.Itoa(i)+".txt", "plainword and some other words in file "+strconv.Itoa(i))
	}
	s0 := time.Now()
	drainText(t, fx.s.textWorker)
	t.Logf("worker: %d plain text files extracted and indexed in %v (%v each)", files, time.Since(s0), time.Since(s0)/files)

	// A 1 MiB text: the database's growth for its row and its full-text row, and the extraction time.
	big := strings.Repeat("שלום לכולם word ", (1<<20)/16)
	fx.file(t, "big-1mib.txt", big)
	before := pageBytes(t, fx.s)
	s0 = time.Now()
	drainText(t, fx.s.textWorker)
	t.Logf("worker: one %d-byte text extracted in %v; the database grew by %d bytes", len(big), time.Since(s0), pageBytes(t, fx.s)-before)

	s0 = time.Now()
	hits := fx.find(t, fx.bobSess, "plainword")
	t.Logf("content search, %d matches (a page of up to 25): %v", len(hits), time.Since(s0))
	s0 = time.Now()
	hits = fx.find(t, fx.bobSess, "word")
	t.Logf("content search of a word in every file (%d matches, a page of up to 25, one 1 MiB snippet): %v", len(hits), time.Since(s0))
}

// pageBytes is the database's size in bytes: its page count times its page size.
func pageBytes(t *testing.T, s *Server) int64 {
	t.Helper()
	var n int64
	err := s.store.Read(context.Background(), func(q store.Querier) error {
		return q.QueryRowContext(context.Background(),
			"SELECT (SELECT page_count FROM pragma_page_count()) * (SELECT page_size FROM pragma_page_size())").Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
