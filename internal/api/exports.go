// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Document previews and exports (design section 14, slice s3, decision 37). A preview is the first page of a document,
// as PDF or as PNG; an export is the whole document in another format. Both are made by Collabora's conversion interface
// (internal/office) on the conversion pool, and kept as derived files of the blob. A conversion is a download, never a node
// in the account's folders (proposal question 2).
//
// Every route answers not_found while office.enabled is off. Access is checked on every call with nodeAccess, as the content
// route checks it. Under the no-download rule a caller who may not download is refused both a preview and an export: a
// preview is a converted copy of the whole document, not a view of one image (decision 37 says why).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/office"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	// previewDispPrefix starts the disposition of a preview's signed link: preview_<format>. Only the server signs it.
	previewDispPrefix = "preview_"
	// exportKindPrefix starts the kind of an export's derived row: export_<format>.
	exportKindPrefix = "export_"
	// convertRetryAfter is the Retry-After, in seconds, of a conversion still being made.
	convertRetryAfter = "2"
)

// convertAdapter is the converter the pool calls: the server's office client, read at each call, so a test can replace it.
// A missing client is an unavailable converter, not a crash.
type convertAdapter struct{ s *Server }

func (a convertAdapter) Convert(ctx context.Context, src io.Reader, size int64, ext, format string, dst io.Writer) error {
	if a.s.conv == nil {
		return office.ErrUnavailable
	}
	return a.s.conv.Convert(ctx, src, size, ext, format, dst)
}

// officeOn reports whether the conversion routes are live: office.enabled, with the client and the pool made. Every
// route and link that serves a conversion checks it first, so a link signed while office was on answers not_found after a
// restart with it off.
func (s *Server) officeOn() bool {
	return s.cfg.Office.Enabled && s.conv != nil && s.convertPool != nil
}

// fileExt returns the lower-case extension of a file name, without its dot, or "" when it has none.
func fileExt(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

// convertKey returns the converter key of the rows made now for a source with extension ext: the build of Collabora, the
// limits, and the extension (decision 37). A copy with another extension keeps its own rows, so one copy cannot make another
// fail. A converter that cannot be asked, or does not offer conversion, answers unavailable.
func (s *Server) convertKey(ctx context.Context, ext string) (string, error) {
	if s.conv == nil {
		return "", convertUnavailable()
	}
	caps, err := s.conv.Capabilities(ctx)
	if err == nil && !caps.Available {
		err = office.ErrUnavailable
	}
	if err != nil {
		return "", convertUnavailable()
	}
	d := s.cfg.Derived
	return derived.ConverterKey(caps, int64(d.MaxSourceBytes), int64(d.MaxConvertBytes)) + "+" + ext, nil
}

// convertUnavailable is the answer when the converter cannot be asked, or does not convert for this server.
func convertUnavailable() error {
	return apierr.New(apierr.Unavailable, "the document converter is not available right now; ask again shortly").
		Retry(time.Minute)
}

// readyFallback returns the newest ready row of a blob's kind made for extension ext, under any build of the converter, or
// nil. A route uses it when the converter's build cannot be had, so a file already made is still served (decision 37).
func (s *Server) readyFallback(ctx context.Context, accountID, blobID, kind, ext string) (*store.Derived, error) {
	rows, err := s.store.DerivedReadyRows(ctx, accountID, blobID, kind)
	if err != nil {
		return nil, err
	}
	for _, d := range rows {
		if strings.HasSuffix(d.Converter, "+"+ext) {
			return d, nil
		}
	}
	return nil, nil
}

// convertSource checks what a conversion of node n would take: its extension must be one the converter's discovery lists for
// conversion (unsupported_media_type otherwise), and its size must be within derived.max_source_bytes (too_large otherwise).
// A discovery document that lists no conversion at all means the converter has none to offer: unavailable, not unsupported.
func (s *Server) convertSource(ctx context.Context, n *store.Node) (string, error) {
	ext := fileExt(n.Name)
	doc, err := s.office.Get(ctx)
	if err != nil || !doc.ConvertsAny() {
		return "", convertUnavailable()
	}
	if !doc.ConvertibleFrom(ext) {
		return "", apierr.New(apierr.UnsupportedMedia, "this type of file cannot be converted").With("reason", "not_convertible")
	}
	if limit := int64(s.cfg.Derived.MaxSourceBytes); n.Size > limit {
		return "", apierr.Newf(apierr.TooLarge, "a document of more than %d bytes cannot be converted", limit).
			With("limit_bytes", limit)
	}
	return ext, nil
}

// convertRefusedGone is the refusal of a conversion whose caller is no longer live. The pool drops the row and no caller
// is told the text; it is the same rule a zip's job applies.
const convertRefusedGone = "the caller of this conversion no longer has access; nothing was made"

// convertGuard is the caller check of a conversion, as a zip's job does it: the caller must still be live and must still reach
// the node, and not under the no-download rule. The pool asks it before the conversion starts and before the file is kept, so
// a caller revoked meanwhile gets no file.
func (s *Server) convertGuard(p *auth.Principal, accountID, nodeID string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := callerActive(ctx, s.store, p, accountID, convertRefusedGone); err != nil {
			return err
		}
		n, role, err := nodeAccessWith(ctx, s.store, p, nodeID)
		if err != nil {
			return err
		}
		restricted, err := s.downloadRestricted(ctx, n, role)
		if err != nil {
			return err
		}
		if restricted {
			return errDownloadRestricted()
		}
		return nil
	}
}

// admitConvert is the per-caller limit on work started: a request that finds no row starts a conversion, and is counted.
func (s *Server) admitConvert(p *auth.Principal) func() error {
	key := string(p.Kind) + ":" + p.ID
	return func() error { return s.rateLimit("convert.enqueue", key) }
}

// convertRequestErr maps the pool's refusal to the API's: a full queue is rate_limited with a retry time.
func convertRequestErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, derived.ErrBusy):
		return apierr.New(apierr.RateLimited, "the conversions are busy; ask again shortly").Retry(5 * time.Second)
	case errors.Is(err, derived.ErrNoBlob):
		return notFound("document")
	}
	return err
}

// previewFormat reads the format query of a preview: pdf (the default), or png for the first page as an image.
func previewFormat(r *http.Request) (string, error) {
	switch f := r.URL.Query().Get("format"); f {
	case "", "pdf":
		return "pdf", nil
	case "png":
		return "png", nil
	default:
		return "", apierr.New(apierr.InvalidRequest, "format must be pdf or png")
	}
}

// previewKind is the derived kind of a preview: pdf, or png_page1 for the first page as an image.
func previewKind(format string) string {
	if format == "png" {
		return "png_page1"
	}
	return "pdf"
}

// previewType is the content type of a preview's file: the server's own, never the source's.
func previewType(format string) string {
	if format == "png" {
		return "image/png"
	}
	return "application/pdf"
}

// previewName is the name a preview's file is shown under: the document's name with the preview's extension.
func previewName(name, format string) string {
	return strings.TrimSuffix(name, path.Ext(name)) + "." + format
}

// handleGetPreview answers a preview of a document: 200 with the file once it is made, 202 while it is being made, 404 when
// there is none to make. On the content origin (content_url set) a ready preview is a 303 to a signed link, as a thumbnail is
// (decision 33). A HEAD request answers from the rows and starts no work. When the converter's build cannot be had, a preview
// already made is still served.
func (s *Server) handleGetPreview(w http.ResponseWriter, r *http.Request) error {
	if !s.officeOn() {
		return notFound("preview")
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return notFound("preview")
	}
	// A preview is a converted copy of the whole document, so the no-download rule refuses it (decision 37).
	restricted, err := s.downloadRestricted(r.Context(), n, role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	format, err := previewFormat(r)
	if err != nil {
		return err
	}
	kind := previewKind(format)
	key, keyErr := s.convertKey(r.Context(), fileExt(n.Name))
	if keyErr != nil {
		d, err := s.readyFallback(r.Context(), n.AccountID, n.BlobID, kind, fileExt(n.Name))
		if err != nil {
			return err
		}
		if d == nil {
			return keyErr
		}
		return s.answerConverted(w, r, n, derived.Answer{State: store.DerivedReady, Row: d, ID: d.ID}, format, kind)
	}
	ext, err := s.convertSource(r.Context(), n)
	if err != nil {
		return err
	}
	if r.Method == http.MethodHead {
		ans, err := s.convertPool.Peek(r.Context(), n.AccountID, n.BlobID, kind, key)
		if err != nil {
			return err
		}
		return s.answerConverted(w, r, n, ans, format, kind)
	}
	p := principalOf(r)
	ans, err := s.convertPool.Request(r.Context(), n.AccountID, n.BlobID, kind, format, ext, key,
		s.convertGuard(p, n.AccountID, n.ID), s.admitConvert(p))
	if err := convertRequestErr(err); err != nil {
		return err
	}
	return s.answerConverted(w, r, n, ans, format, kind)
}

// answerConverted answers a preview from its answer: a ready file, a 303 to its signed link on the content origin, 202 while
// it is made, or 404 when the conversion failed.
func (s *Server) answerConverted(w http.ResponseWriter, r *http.Request, n *store.Node, ans derived.Answer, format, kind string) error {
	switch ans.State {
	case store.DerivedReady:
		if s.cfg.ContentURL != "" {
			c := linkClaims{nod: n.ID, acc: n.AccountID, disp: previewDispPrefix + format,
				exp: time.Now().Add(s.cfg.Links.DefaultTTL.D()).Unix()}
			w.Header().Set("Cache-Control", "private, no-cache")
			w.Header().Set("Location", s.cfg.ContentURL+linkPrefix+s.links.sign(c, n.ContentTag))
			w.WriteHeader(http.StatusSeeOther)
			return nil
		}
		return s.writeConverted(w, r, n.AccountID, ans.Row, previewType(format), dispInline, previewName(n.Name, format),
			corpSameOrigin)
	case store.DerivedPending:
		w.Header().Set("Retry-After", convertRetryAfter)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return nil
	}
	return notFound("preview")
}

// serveConvertedLink answers a signed preview link on the content origin. The link was checked when it was signed and is
// checked again in handleGetLink, so only the row and its file are looked up here. It is cross-origin, as a signed content
// link is: other sites embed it (links.go). With office off it is not found, as the routes are (decision 37).
func (s *Server) serveConvertedLink(w http.ResponseWriter, r *http.Request, n *store.Node, disp string) error {
	if !s.officeOn() {
		return linkNotFound()
	}
	format, ok := strings.CutPrefix(disp, previewDispPrefix)
	if !ok || !slices.Contains([]string{"pdf", "png"}, format) {
		return linkNotFound()
	}
	ext := fileExt(n.Name)
	kind := previewKind(format)
	key, keyErr := s.convertKey(r.Context(), ext)
	var d *store.Derived
	if keyErr == nil {
		ans, err := s.convertPool.Peek(r.Context(), n.AccountID, n.BlobID, kind, key)
		if err != nil {
			return err
		}
		if ans.State == store.DerivedReady {
			d = ans.Row
		}
	} else {
		fb, err := s.readyFallback(r.Context(), n.AccountID, n.BlobID, kind, ext)
		if err != nil {
			return err
		}
		d = fb
	}
	if d == nil {
		return linkNotFound()
	}
	return s.writeConverted(w, r, n.AccountID, d, previewType(format), dispInline, previewName(n.Name, format), corpCrossOrigin)
}

// exportFormats is the fixed list of export formats. A format outside it is refused before anything is queued. The preview
// formats are not exports: a PNG of the first page is a preview.
var exportFormats = []string{"docx", "odp", "ods", "odt", "pdf", "pptx", "xlsx"}

// handleCreateExport queues an export of a document in another format. It answers 202 with the export's ID and the Location of
// its file, which answers 202 while it is made and the file when it is ready. An export already made, or already queued, is
// not queued again: the answer names its ID, as the first answer did.
func (s *Server) handleCreateExport(w http.ResponseWriter, r *http.Request) error {
	if !s.officeOn() {
		return notFound("export")
	}
	var in struct {
		Format string `json:"format"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if !slices.Contains(exportFormats, in.Format) {
		return apierr.New(apierr.InvalidRequest, "format must be one of "+strings.Join(exportFormats, ", ")).
			With("reason", "format")
	}
	nodeID := r.PathValue("nod")
	n, role, err := s.nodeAccess(r, nodeID)
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return notFound("export")
	}
	// A download in another format is refused under the no-download rule, as a copy is (decision 20).
	restricted, err := s.downloadRestricted(r.Context(), n, role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	ext, err := s.convertSource(r.Context(), n)
	if err != nil {
		return err
	}
	key, err := s.convertKey(r.Context(), ext)
	if err != nil {
		return err
	}
	p := principalOf(r)
	ans, err := s.convertPool.Request(r.Context(), n.AccountID, n.BlobID, exportKindPrefix+in.Format, in.Format, ext, key,
		s.convertGuard(p, n.AccountID, n.ID), s.admitConvert(p))
	if err := convertRequestErr(err); err != nil {
		return err
	}
	if ans.State == store.DerivedFailed {
		return apierr.New(apierr.UnsupportedMedia, "this document could not be exported to that format").With("reason", "failed")
	}
	w.Header().Set("Location", "/api/v1/nodes/"+nodeID+"/exports/"+ans.ID)
	w.Header().Set("Retry-After", convertRetryAfter)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]string{"id": ans.ID, "state": ans.State, "format": in.Format})
	return nil
}

// handleGetExport sends an export to anyone who may read the document: 200 with the file (Range supported), 202 while it is
// made, 404 otherwise. Access and the no-download rule are checked on every call, as a zip's download checks them. When the
// converter's build cannot be had, an export already made for this extension is still served.
func (s *Server) handleGetExport(w http.ResponseWriter, r *http.Request) error {
	if !s.officeOn() {
		return notFound("export")
	}
	n, role, err := s.nodeAccess(r, r.PathValue("nod"))
	if err != nil {
		return err
	}
	if n.Kind != store.KindFile {
		return notFound("export")
	}
	restricted, err := s.downloadRestricted(r.Context(), n, role)
	if err != nil {
		return err
	}
	if restricted {
		return errDownloadRestricted()
	}
	// The row must be this account's and must belong to this node's blob, so an ID from another file or account is not found.
	d, err := s.store.DerivedByID(r.Context(), n.AccountID, r.PathValue("drv"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && (d.BlobID != n.BlobID || !strings.HasPrefix(d.Kind, exportKindPrefix))) {
		return notFound("export")
	}
	if err != nil {
		return err
	}
	ext := fileExt(n.Name)
	key, keyErr := s.convertKey(r.Context(), ext)
	switch {
	case keyErr != nil:
		// The build cannot be had: only a file already made for this extension is served.
		if d.State != store.DerivedReady || !strings.HasSuffix(d.Converter, "+"+ext) {
			return keyErr
		}
	case d.Converter != key:
		// A row made by another build or other limits, or for another extension, is not served as current.
		return notFound("export")
	}
	format := strings.TrimPrefix(d.Kind, exportKindPrefix)
	switch d.State {
	case store.DerivedReady:
		ctype, _ := office.ContentType(format)
		return s.writeConverted(w, r, n.AccountID, d, ctype, dispAttachment, previewName(n.Name, format), corpSameOrigin)
	case store.DerivedPending:
		w.Header().Set("Retry-After", convertRetryAfter)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, map[string]string{"state": "pending"})
		return nil
	}
	return notFound("export")
}

// writeConverted sends the file of a ready conversion with the headers every content response carries. The type is the
// server's, from the format asked for; the ETag is the file's SHA-256, so an unchanged file is a 304. corp is the
// Cross-Origin-Resource-Policy: cross-origin on a signed link, which other sites embed, and same-origin otherwise. The Range
// header is checked by applyRange, the content route's rule (at most maxRanges ranges), and then http.ServeContent answers it,
// so an interrupted download resumes.
func (s *Server) writeConverted(w http.ResponseWriter, r *http.Request, accountID string, d *store.Derived, ctype, disp, name, corp string) error {
	f, err := s.convertPool.Open(accountID, d.ID)
	if err != nil {
		return notFound("file")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the converted file cannot be read", err)
	}
	if fi.Size() != d.Size {
		return apierr.Wrap(apierr.Internal, "the converted file does not match its size",
			fmt.Errorf("derived %s holds %d bytes, the row says %d", d.ID, fi.Size(), d.Size))
	}
	etag := `"` + d.SHA256 + `"`
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", contentCSP)
	h.Set("Cross-Origin-Resource-Policy", corp)
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", contentDisposition(disp, name))
	h.Set("ETag", etag)
	if err := applyRange(h, r, etag, time.Time{}, fi.Size()); err != nil {
		return err
	}
	http.ServeContent(w, r, name, time.Time{}, f)
	s.convertPool.Cache().Use(accountID, d.ID)
	return nil
}
