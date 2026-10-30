// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Uploads through a drop link (design section 8, Share links). A visitor of a drop link may upload a file into the
// link's directory and nothing else. The visitor reaches no node: its access is the upload's own, checked on every
// request and inside every write, against the link as it is then. Every visitor of one link shares the link's
// principal, so each upload carries its visitor's ID and no other visitor reaches it.

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The uploader's name and email are at most these many characters. The email is never verified.
const (
	dropNameMax  = 100
	dropEmailMax = 254
	// dropMaxOpen is how many unfinished uploads a drop link holds at once, across all its visitors.
	dropMaxOpen = 20
)

// releaseDropUploads ends the open uploads of a drop link in the transaction that ends the link: their rows
// and reservations go with it. The uploads it returns still have their files; the caller removes them with
// discardUploadFile once the transaction has committed (slice o3, item 4).
func (s *Server) releaseDropUploads(r *http.Request, tx *store.Tx, l *store.ShareLink) ([]*store.Upload, error) {
	if !l.UploadOnly {
		return nil, nil
	}
	ups, err := tx.UploadsCreatedBy(r.Context(), l.AccountID, l.ID, string(auth.KindShareLink))
	if err != nil {
		return nil, err
	}
	for _, up := range ups {
		if err := s.removeUploadTx(r, tx, up, "link_gone"); err != nil {
			return nil, err
		}
	}
	return ups, nil
}

// isDrop reports whether p is a drop link's visitor.
func isDrop(p *auth.Principal) bool {
	return p != nil && p.Kind == auth.KindShareLink && p.Link != nil && p.Link.UploadOnly
}

// dropUploadAccess checks that a drop link's visitor may act on an upload now: the upload is the visitor's own, it
// lands in the link's directory, and the link is still live, which the link's state read here decides. A link that
// is gone, reset, expired, paused, trashed or that the visitor's credential predates answers not_found, the one answer
// a dead link gets. It runs inside every transaction that touches the upload, so a change made mid-upload stops the
// next step.
func dropUploadAccess(ctx context.Context, rd accessReader, p *auth.Principal, up *store.Upload) error {
	if p.Link.VisitorID == "" || up.VisitorID != p.Link.VisitorID || up.ParentID != p.Link.Node {
		return notFound("upload")
	}
	st, err := rd.ShareLinkStateByID(ctx, p.AccountID, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("link")
	}
	if err != nil {
		return err
	}
	if !st.Link.UploadOnly || st.Link.NodeID != p.Link.Node || !shareOpenable(st, time.Now()) ||
		!p.Link.IssuedAt.After(st.Link.CredentialsAt) {
		return notFound("link")
	}
	self, above, err := rd.TrashState(ctx, p.AccountID, st.Link.NodeID)
	if err != nil {
		return err
	}
	if self || above {
		return notFound("link")
	}
	return nil
}

// dropLinkGone reports whether a drop link has truly ended for a visitor whose credential was issued at issued: its
// row is gone, it has expired, or it was reset after that credential was issued. A link that is only paused, or
// whose directory is trashed, is not gone: its uploads wait for it, and resume when it is back. A read that fails
// counts as not gone, so a fault never ends an upload.
func (s *Server) dropLinkGone(ctx context.Context, accountID, linkID string, issued time.Time) bool {
	st, err := s.store.ShareLinkStateByID(ctx, accountID, linkID)
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	if !issued.After(st.Link.CredentialsAt) {
		return true
	}
	return st.Link.ExpiresAt != nil && !time.Now().Before(*st.Link.ExpiresAt)
}

// dropEnded ends a drop visitor's upload when a request for it was refused because its link is dead: the row and
// the file go, so the upload's reservation is released at once. It returns err unchanged. Only the upload's own
// visitor ends it. Any other error passes through untouched.
func (s *Server) dropEnded(r *http.Request, p *auth.Principal, up *store.Upload, err error) error {
	if err == nil || !isDrop(p) || up.VisitorID != p.Link.VisitorID || apierr.From(err).Code != apierr.NotFound {
		return err
	}
	if !s.dropLinkGone(r.Context(), p.AccountID, p.ID, p.Link.IssuedAt) {
		return err
	}
	werr := s.store.Write(context.WithoutCancel(r.Context()), func(tx *store.Tx) error {
		return s.removeUploadTx(r, tx, up, "link_gone")
	})
	if werr != nil {
		// The row stays, so its file stays with it: the file goes only with the row.
		s.log.Warn("cannot end an upload of a dead link", "upload", up.ID, "error", werr)
		return err
	}
	s.discardUploadFile(up)
	return err
}

// dropTarget reads a drop visitor's upload metadata. The visitor names no folder and no directory: the upload goes to
// the link's directory, so folder, parent, on_conflict and mime_type that the visitor sends are ignored, and a path or
// parents is refused. The name clash is always settled by renaming, so the visitor learns no existing name. The
// visitor's name and email are read here as well, as the link asks for them.
func (s *Server) dropTarget(ctx context.Context, p *auth.Principal, meta map[string]string) (*uploadTarget, *store.ShareLinkState, error) {
	if path := meta["path"]; path != "" {
		return nil, nil, apierr.New(apierr.InvalidRequest, "a drop link takes files into its own directory; path is not allowed")
	}
	if v := meta["parents"]; v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, nil, apierr.New(apierr.InvalidRequest, "parents must be true or false")
		}
		if on {
			return nil, nil, apierr.New(apierr.InvalidRequest, "a drop link takes files into its own directory; parents is not allowed")
		}
	}
	st, err := s.store.ShareLinkStateByID(ctx, p.AccountID, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, notFound("link")
	}
	if err != nil {
		return nil, nil, err
	}
	if !st.Link.UploadOnly || !shareOpenable(st, time.Now()) {
		return nil, nil, notFound("link")
	}
	forced := map[string]string{}
	for k, v := range meta {
		forced[k] = v
	}
	forced["folder"] = st.FolderID
	forced["parent"] = st.Link.NodeID
	forced["on_conflict"] = "rename"
	delete(forced, "path")
	delete(forced, "parents")
	delete(forced, "mime_type")
	tgt, err := readTarget(forced)
	if err != nil {
		return nil, nil, err
	}
	return tgt, st, nil
}

// uploaderFields reads the name and email a visitor gave. Both are required when the link asks for them, and optional
// otherwise. A line break is not kept. The email must be a bare address, and is never verified or mailed.
func uploaderFields(meta map[string]string, askName bool) (name, email string, err error) {
	name = cleanLine(meta["uploader_name"])
	email = cleanLine(meta["uploader_email"])
	if askName && name == "" {
		return "", "", apierr.New(apierr.InvalidRequest, "the name is required for this link")
	}
	if askName && email == "" {
		return "", "", apierr.New(apierr.InvalidRequest, "the email is required for this link")
	}
	if utf8.RuneCountInString(name) > dropNameMax {
		return "", "", apierr.Newf(apierr.InvalidRequest, "the name may be at most %d characters", dropNameMax)
	}
	if utf8.RuneCountInString(email) > dropEmailMax {
		return "", "", apierr.Newf(apierr.InvalidRequest, "the email may be at most %d characters", dropEmailMax)
	}
	if email != "" {
		addr, perr := mail.ParseAddress(email)
		if perr != nil || addr.Address != email || strings.ContainsAny(email, " <>") {
			return "", "", apierr.New(apierr.InvalidRequest, "the email is not an address")
		}
	}
	return name, email, nil
}

// endDroppedUploadOnDeadLink ends the upload a drop visitor's request names when the visitor's credential is refused
// because its link is dead: deleted, reset, expired, paused or trashed. The request never reaches the handler, so the
// upload would otherwise keep its reservation until the sweep. The credential's MAC is checked again here, and only
// the visitor it was made for can end its own upload. A credential refused for expiry alone is not a dead link, and
// ends nothing.
func (s *Server) endDroppedUploadOnDeadLink(r *http.Request) {
	cred, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(cred, auth.SharePrefix) {
		return
	}
	body, mac, err := splitShareCredential(cred)
	if err != nil || s.shares.match(body, mac) == nil {
		return
	}
	pl, err := parseCredentialPayload(body)
	if err != nil || !pl.valid() || pl.VisitorID == "" {
		return
	}
	uplID := r.PathValue("upl")
	if !id.Valid("upl", uplID) {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	up, err := s.store.Upload(ctx, pl.AccountID, uplID)
	if err != nil || up.CreatedBy != pl.LinkID || up.CreatedKind != string(auth.KindShareLink) || up.VisitorID != pl.VisitorID {
		return
	}
	if !s.dropLinkGone(ctx, pl.AccountID, pl.LinkID, time.UnixMilli(pl.IssuedAt)) {
		return
	}
	werr := s.store.Write(ctx, func(tx *store.Tx) error {
		return s.removeUploadTx(r, tx, up, "link_gone")
	})
	if werr != nil {
		s.log.Warn("cannot end an upload of a dead link", "upload", up.ID, "error", werr)
		return
	}
	s.discardUploadFile(up)
}

// dropNodeOwner is the owner a completed drop upload's node reports. It is the owner InsertNode gives the node:
// a personal folder's user, else the creator (slice o3, item 6).
func dropNodeOwner(f *store.Folder, creator string) string {
	if f.Kind == store.KindPersonal && f.OwnerUserID != "" {
		return f.OwnerUserID
	}
	return creator
}

// dropRefusal is the one answer a drop visitor gets when the account's quota or the disk cannot take what it
// sends: no numbers and no scope, so the visitor learns nothing of the account's space or the disk (item 2).
// Any other error passes through, and so does every error for a principal that is not a drop visitor.
func dropRefusal(p *auth.Principal, err error) error {
	if !isDrop(p) || err == nil {
		return err
	}
	switch apierr.From(err).Code {
	case apierr.QuotaExceeded, apierr.StorageFull:
		return apierr.New(apierr.QuotaExceeded, "this link cannot take the file now")
	}
	return err
}

// pieceBytes is how many bytes a piece will bring: the request's declared length when it is shorter than the piece
// cap, else the cap. An unknown length is taken as the cap.
func pieceBytes(r *http.Request, chunkCap int64) int64 {
	if r.ContentLength >= 0 && r.ContentLength < chunkCap {
		return r.ContentLength
	}
	return chunkCap
}

// dropFlight counts the bytes of drop pieces whose bodies are still arriving, per account and in all. A piece is
// checked against the quota and the disk with these counted, so pieces in flight cannot each pass on the same space
// (slice o3, round 2, item 1). The count is kept under mu from the check to the release.
type dropFlight struct {
	mu        sync.Mutex
	total     int64
	byAccount map[string]int64
}

// admitDropPiece checks a drop piece of bytes against the quota and the disk, counting the drop pieces in flight, and
// on success counts the piece as in flight. The returned func releases it; call it once the piece is recorded or has
// failed.
func (s *Server) admitDropPiece(ctx context.Context, up *store.Upload, bytes int64) (func(), error) {
	f := &s.dropFlight
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := s.checkDropPiece(ctx, up, bytes, f.total, f.byAccount[up.AccountID]); err != nil {
		return nil, err
	}
	if f.byAccount == nil {
		f.byAccount = map[string]int64{}
	}
	f.total += bytes
	f.byAccount[up.AccountID] += bytes
	acc := up.AccountID
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.total -= bytes
		if f.byAccount[acc] -= bytes; f.byAccount[acc] == 0 {
			delete(f.byAccount, acc)
		}
	}, nil
}

// checkDropPiece refuses a drop visitor's piece that the account's quota or the disk cannot take for the bytes it
// brings. flightAll and flightAcc are the drop bytes of pieces in flight, all accounts and this one. The check is
// made before the body is read; recordPiece makes the same check in the write that records the bytes. The visitor's
// earlier bytes stay, so it resumes once space frees.
//
// The disk rules are those of other uploads: the install-wide pool must fit, and the account's share of the disk
// applies (upload.max_disk_share_percent), so anonymous visitors cannot fill the disk down to storage.min_free.
func (s *Server) checkDropPiece(ctx context.Context, up *store.Upload, bytes, flightAll, flightAcc int64) error {
	acc, err := s.store.Account(ctx, up.AccountID)
	if err != nil {
		return err
	}
	if err := s.checkQuota(acc, store.UsageChange{Reserved: bytes + flightAcc}); err != nil {
		return err
	}
	// A drop into a personal folder is charged to its user, so that user's limit applies too. The visitor learns
	// nothing of it: dropRefusal gives the one generic answer.
	st, err := s.store.QuotaStateOf(ctx, up.AccountID, up.FolderID)
	if err != nil {
		return err
	}
	if err := s.checkUserLimit(nil, up.AccountID, st, bytes+flightAcc); err != nil {
		return err
	}
	usable, ok, err := s.diskUsable()
	if err != nil || !ok {
		return err
	}
	_, total, err := s.store.UnwrittenBytes(ctx, up.AccountID)
	if err != nil {
		return err
	}
	held, err := s.store.DropBytesHeld(ctx, up.AccountID)
	if err != nil {
		return err
	}
	// The piece is held to the account's share as a creation is: its bytes and the account's held and in-flight
	// drop bytes count against the share.
	return s.checkDisk(usable, held+flightAcc+bytes, total+flightAll+bytes, true)
}
