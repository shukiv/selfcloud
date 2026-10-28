package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"selfcloud/internal/auth"
	"selfcloud/internal/config"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
)

// patternReader produces n bytes by repeating a block, so the benchmark makes no large buffers of
// its own.
type patternReader struct {
	block []byte
	left  int64
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(b)), p.left, int64(len(p.block)))
	copy(b, p.block[:n])
	p.left -= n
	return int(n), nil
}

// BenchmarkUpload pushes 1 GiB through the upload routes in 64 MiB pieces, each a PATCH through the
// real handler, into a temporary data directory, and reports MB/s. It needs about 1 GiB of free
// space in TMPDIR, so point TMPDIR at a disk when /tmp is a small tmpfs. Run it with:
//
//	TMPDIR=/var/tmp go test ./internal/api -run '^$' -bench BenchmarkUpload -benchtime 1x
func BenchmarkUpload(b *testing.B) {
	const total = int64(1) << 30
	const chunk = int64(64) << 20
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.DataDir = b.TempDir()
	cfg.Storage.MinFree = 0
	cfg.Store.Path = filepath.Join(cfg.DataDir, "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: cfg.Store.Path})
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	s, err := New(Options{Config: cfg, Store: st})
	if err != nil {
		b.Fatal(err)
	}

	// An account, a folder and an account key, written straight to the store.
	now := store.Now()
	acc := &store.Account{ID: id.New("acc"), Name: "bench", Status: store.AccountActive, CreatedAt: now, UpdatedAt: now,
		TokensNotBefore: now}
	fld := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Bench", NameKey: "bench", CreatedAt: now, UpdatedAt: now}
	secret, prefix, hash := auth.NewKey(auth.AccountKeyPrefix)
	key := &store.APIKey{ID: id.New("key"), AccountID: acc.ID, Name: "bench", Prefix: prefix, Hash: hash, CreatedAt: now}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		if err := tx.InsertFolder(ctx, fld, "bench"); err != nil {
			return err
		}
		return tx.InsertAPIKey(ctx, key)
	}); err != nil {
		b.Fatal(err)
	}

	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte(i)
	}
	send := func(method, path string, hdr map[string]string, body io.Reader, length int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		req.ContentLength = length
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Tus-Resumable", tusVersion)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	meta := md("folder", fld.ID, "name", "big.bin")

	b.SetBytes(total)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := send("POST", "/api/v1/uploads", map[string]string{
			"Upload-Length": strconv.FormatInt(total, 10), "Upload-Metadata": meta}, nil, 0)
		if rec.Code != http.StatusCreated {
			b.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		upl := rec.Header().Get("Location")
		upl = upl[strings.LastIndex(upl, "/")+1:]
		var off int64
		var last *httptest.ResponseRecorder
		for off < total {
			n := min(chunk, total-off)
			last = send("PATCH", "/api/v1/uploads/"+upl, map[string]string{
				"Content-Type": uploadBodyType, "Upload-Offset": strconv.FormatInt(off, 10)},
				&patternReader{block: block, left: n}, n)
			if last.Code != http.StatusNoContent {
				b.Fatalf("piece at %d: %d %s", off, last.Code, last.Body)
			}
			off += n
		}
		if last.Header().Get(nodeHeader) == "" {
			b.Fatal("the last piece did not complete the file")
		}
		// Each iteration keeps its file; the temporary directory goes at the end.
	}
}
