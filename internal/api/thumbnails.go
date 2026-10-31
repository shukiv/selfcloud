// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Thumbnails (design section 14, slice s1; decision 33). A thumbnail is a derived file of a blob, made on
// request, never on upload, and cached by blob, so a new version of a file gets new thumbnails.
//
// The route reads the file's access the way the content route does, on every call (nodeAccess). A share-link
// visitor gets link_route, as the route carries no link flag (the proposal, section 3). A thumbnail is viewing,
// not a download, so the no-download rule does not refuse it (decision 20; proposal question 9).
//
// On the content origin (content_url set), the main origin does not show inline content, so the thumbnail is
// fetched there through a signed link: the route answers 303 to it (proposal question 6). The link carries
// the disposition thumb_<size>, which only the server signs.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
)

// thumbDispPrefix starts the disposition of a thumbnail's signed link: thumb_<size>.
const thumbDispPrefix = "thumb_"

// thumbBatch is how many nodes one has_thumbnail query names at most.
const thumbBatch = 500

// newDerivedPool builds the pool that makes derived files. Its files live under derived.dir, or
// data_dir/derived. The server's own binary runs the decoder subcommand.
func newDerivedPool(cfg *config.Config, st *store.Store, blobs blob.Store, log *slog.Logger) (*derived.Pool, error) {
	dir := cfg.Derived.Dir
	if dir == "" {
		dir = filepath.Join(cfg.DataDir, "derived")
	}
	files, err := derived.NewFiles(dir)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("derived: the server's own binary: %w", err)
	}
	// The decoder's sandbox is applied by each child itself; the server says once what the kernel gives it.
	if abi := thumb.LandlockABI(); abi == 0 {
		log.Warn("thumbnail decoder: this kernel has no Landlock; the decoder keeps the server's file and network " +
			"access, under no_new_privs and its limits only")
	} else {
		log.Info("thumbnail decoder sandbox", "landlock_abi", abi)
	}
	d := cfg.Derived
	cache := derived.NewCache(st, files, int64(d.MaxBytes), log)
	return derived.NewPool(derived.Options{Store: st, Blobs: blobs, Cache: cache, Exe: exe,
		Workers: d.Workers, Queue: d.Queue, Timeout: d.Timeout.D(), CPUSeconds: int64(d.CPUSeconds),
		MemoryBytes: int64(d.MemoryBytes), MaxPixels: d.MaxPixels, MaxSourceBytes: int64(d.MaxSourceBytes),
		MaxOutputBytes: 16 << 20, MinFree: uint64(cfg.Storage.MinFree), Log: log}), nil
}

// smallestThumb is the smallest configured thumbnail size: the default a request takes, and the one has_thumbnail
// refers to.
func (s *Server) smallestThumb() int {
	m := s.cfg.Derived.ThumbSizes[0]
	for _, v := range s.cfg.Derived.ThumbSizes[1:] {
		m = min(m, v)
	}
	return m
}

// thumbnailSize reads the size query: one of the configured sizes, the smallest when absent.
func (s *Server) thumbnailSize(r *http.Request) (int, error) {
	q := r.URL.Query().Get("size")
	if q == "" {
		return s.smallestThumb(), nil
	}
	if n, err := strconv.Atoi(q); err == nil {
		for _, v := range s.cfg.Derived.ThumbSizes {
			if v == n {
				return n, nil
			}
		}
	}
	sizes := make([]string, len(s.cfg.Derived.ThumbSizes))
	for i, v := range s.cfg.Derived.ThumbSizes {
		sizes[i] = strconv.Itoa(v)
	}
	return 0, apierr.Newf(apierr.InvalidRequest, "size must be one of %s", strings.Join(sizes, ", "))
}

// handleGetThumbnail answers a thumbnail of an image file the caller may read: 200 with the image when it is
// ready, 202 while it is being made, 404 when there is none to make, and 429 when the queue or the caller's
// budget is full.
func (s *Server) handleGetThumbnail(w http.ResponseWriter, r *http.Request) error {
	if !thumb.Supported {
		return apierr.New(apierr.Unavailable, "thumbnails need the Linux server: its decoder limits are enforced there only")
	}
	n, _, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	// A folder, a shortcut and a file of another type have no thumbnail.
	if n.Kind != store.KindFile || !thumb.Eligible(n.MimeType) {
		return notFound("thumbnail")
	}
	size, err := s.thumbnailSize(r)
	if err != nil {
		return err
	}
	if limit := int64(s.cfg.Derived.MaxSourceBytes); n.Size > limit {
		return apierr.Newf(apierr.TooLarge, "an image of more than %d bytes has no thumbnail", limit).
			With("limit_bytes", limit)
	}
	// A HEAD request reads and never starts work: it answers what a GET would find, and queues nothing.
	if r.Method == http.MethodHead {
		ans, err := s.derivedPool.Peek(r.Context(), n.AccountID, n.BlobID, size)
		if err != nil {
			return err
		}
		switch ans.State {
		case store.DerivedReady:
			return s.serveThumbnail(w, r, n, ans.Row)
		case store.DerivedPending:
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusAccepted)
			return nil
		}
		return notFound("thumbnail")
	}
	p := principalOf(r)
	key := string(p.Kind) + ":" + p.ID
	ans, err := s.derivedPool.Request(r.Context(), n.AccountID, n.BlobID, size,
		func() error { return s.rateLimit("thumb.enqueue", key) })
	switch {
	case errors.Is(err, derived.ErrBusy):
		w.Header().Set("Retry-After", "5")
		return apierr.New(apierr.RateLimited, "the thumbnails are busy; ask again shortly")
	case errors.Is(err, derived.ErrNoBlob):
		return notFound("thumbnail")
	case err != nil:
		return err
	}
	switch ans.State {
	case store.DerivedReady:
		return s.serveThumbnail(w, r, n, ans.Row)
	case store.DerivedFailed:
		return notFound("thumbnail")
	}
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
	return nil
}

// serveThumbnail answers a ready thumbnail: the image itself, or on a content origin, a redirect to a signed link.
func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, n *store.Node, d *store.Derived) error {
	if s.cfg.ContentURL != "" {
		c := linkClaims{nod: n.ID, acc: n.AccountID, disp: d.Kind,
			exp: time.Now().Add(s.cfg.Links.DefaultTTL.D()).Unix()}
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("Location", s.cfg.ContentURL+linkPrefix+s.links.sign(c, n.ContentTag))
		w.WriteHeader(http.StatusSeeOther)
		return nil
	}
	return s.writeThumbnail(w, r, n.AccountID, d, corpSameOrigin)
}

// writeThumbnail sends the file of a ready thumbnail with the headers every content response carries. The type is
// the server's: a JPEG, whatever the source was. The ETag is the file's SHA-256, so an unchanged thumbnail is a 304.
func (s *Server) writeThumbnail(w http.ResponseWriter, r *http.Request, accountID string, d *store.Derived, corp string) error {
	f, err := s.derivedPool.Open(accountID, d.ID)
	if err != nil {
		// The row says ready and the file is not there. Request clears such a row and makes the file again.
		return notFound("thumbnail")
	}
	defer f.Close()
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", contentCSP)
	h.Set("Cross-Origin-Resource-Policy", corp)
	h.Set("Content-Type", "image/jpeg")
	h.Set("ETag", `"`+d.SHA256+`"`)
	http.ServeContent(w, r, "thumbnail.jpg", time.Time{}, f)
	s.derivedPool.Cache().Use(accountID, d.ID)
	return nil
}

// serveThumbLink answers a signed thumbnail link on the content origin. The link was checked when it was signed and
// is checked again here (handleGetLink), so only the file is looked up.
func (s *Server) serveThumbLink(w http.ResponseWriter, r *http.Request, n *store.Node, kind string) error {
	d, err := s.derivedPool.Cache().Lookup(r.Context(), n.AccountID, n.BlobID, kind, s.derivedPool.Converter())
	if err != nil {
		return err
	}
	if d == nil || d.State != store.DerivedReady {
		return linkNotFound()
	}
	return s.writeThumbnail(w, r, n.AccountID, d, corpCrossOrigin)
}

// validLinkDisposition is validDisposition with the thumbnail kinds added. Only the server signs a thumb_ link, and
// handleCreateLink still accepts only validDisposition.
func validLinkDisposition(d string) bool {
	if validDisposition(d) {
		return true
	}
	if rest, ok := strings.CutPrefix(d, previewDispPrefix); ok {
		return rest == "pdf" || rest == "png"
	}
	rest, ok := strings.CutPrefix(d, thumbDispPrefix)
	return ok && len(rest) > 0 && len(rest) <= 4 && allDigits(rest)
}

// withThumbnails sets has_thumbnail on the image files among items: true when a thumbnail of the smallest size is ready.
// A link visitor gets none (decision 24: a visitor sees no more than the item's own content), and other kinds get none.
// It is one query per batch of nodes, so a listing page costs one read.
func (s *Server) withThumbnails(ctx context.Context, p *auth.Principal, accountID string, items ...*nodeJSON) error {
	if p == nil || p.Kind == auth.KindShareLink {
		return nil
	}
	var ids []string
	var among []*nodeJSON
	for _, it := range items {
		if it.Kind == store.KindFile && thumb.Eligible(it.MimeType) {
			no := false
			it.HasThumbnail = &no
			ids = append(ids, it.ID)
			among = append(among, it)
		}
	}
	kind := derived.Kind(s.smallestThumb())
	for start := 0; start < len(ids); start += thumbBatch {
		end := min(start+thumbBatch, len(ids))
		ready, err := s.store.DerivedThumbs(ctx, accountID, ids[start:end], kind, s.derivedPool.Converter())
		if err != nil {
			return err
		}
		for _, it := range among[start:end] {
			if ready[it.ID] {
				yes := true
				it.HasThumbnail = &yes
			}
		}
	}
	return nil
}
