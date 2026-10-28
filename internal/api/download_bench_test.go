// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// BenchmarkDownload reads a 1 GiB file over loopback HTTP, through the real handler and a real
// listener, so the response leaves the server by sendfile(2) where the platform allows it. It
// reports MB/s for two shapes: the whole file in one GET, and the file in sixteen 64 MiB ranges.
// The client reads into io.Discard on the same machine, so the number includes its work.
//
// It needs about 1 GiB of free space in TMPDIR, so point TMPDIR at a disk when /tmp is a small
// tmpfs. Run it with:
//
//	TMPDIR=/var/tmp go test ./internal/api -run '^$' -bench BenchmarkDownload -benchtime 1x
func BenchmarkDownload(b *testing.B) {
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

	// The 1 GiB file is written once, outside the timer, as a blob and a node.
	block := make([]byte, 1<<20)
	for i := range block {
		block[i] = byte(i)
	}
	wr, err := s.blobs.Create(ctx, acc.ID, total)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := io.Copy(wr, &patternReader{block: block, left: total}); err != nil {
		b.Fatal(err)
	}
	c, err := wr.Commit()
	if err != nil {
		b.Fatal(err)
	}
	root, err := st.RootNode(ctx, acc.ID, fld.ID)
	if err != nil {
		b.Fatal(err)
	}
	file := &store.Node{ID: id.New("nod"), AccountID: acc.ID, FolderID: fld.ID, ParentID: root.ID,
		Name: "big.bin", NameKey: "big.bin", Kind: store.KindFile, Size: total, MimeType: "application/octet-stream",
		BlobID: c.ID, ETag: newETag(), SHA256: c.SHA256, CreatedAt: now, ModifiedAt: now, CreatedBy: key.ID,
		ModifiedBy: key.ID}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: acc.ID, Size: c.Size, SHA256: c.SHA256,
			State: store.BlobLive, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertNode(ctx, file)
	}); err != nil {
		b.Fatal(err)
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := ts.Client()
	url := ts.URL + "/api/v1/nodes/" + file.ID + "/content"

	// get reads one response in full and returns its body length.
	get := func(b *testing.B, rng string) int64 {
		b.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		want := http.StatusOK
		if rng != "" {
			req.Header.Set("Range", rng)
			want = http.StatusPartialContent
		}
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			b.Fatalf("GET %q: %d", rng, resp.StatusCode)
		}
		n, err := io.Copy(io.Discard, resp.Body)
		if err != nil {
			b.Fatal(err)
		}
		return n
	}

	b.Run("whole_file", func(b *testing.B) {
		b.SetBytes(total)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if n := get(b, ""); n != total {
				b.Fatalf("read %d bytes, want %d", n, total)
			}
		}
	})
	b.Run("ranges_64MiB", func(b *testing.B) {
		b.SetBytes(total)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for off := int64(0); off < total; off += chunk {
				want := chunk
				if n := get(b, fmt.Sprintf("bytes=%d-%d", off, off+chunk-1)); n != want {
					b.Fatalf("range at %d: read %d bytes, want %d", off, n, want)
				}
			}
		}
	})
}
