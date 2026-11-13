// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

const (
	tusVersion        = "1.0.0"
	uploadsRoute      = "/api/v1/uploads"
	pieceType         = "application/offset+octet-stream"
	nodeIDHeader      = "Selfcloud-Node-ID"
	uploadStateHeader = "Selfcloud-Upload-State"
	awaitingCommit    = "awaiting_commit"
	infoRoute         = "/api/v1/info"
)

// uploadRoute is the route of one upload, with a suffix such as "/commit".
func uploadRoute(uplID, suffix string) string { return uploadsRoute + "/" + uplID + suffix }

// tusHeader is the header every tus request carries.
func tusHeader() http.Header { return http.Header{"Tus-Resumable": {tusVersion}} }

// errAwaiting is an upload whose bytes are all on the server and that waits for a name.
var errAwaiting = errors.New("the upload waits for a name")

// Put is `selfcloud put LOCAL [DIR]` and `selfcloud put --resume ID`: a resumable upload by tus (decision 142).
func Put(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("put")
	var g conn.Global
	g.Register(set)
	var tf transferFlags
	tf.register(set)
	var folder, name, resume string
	var parents, replace, rename bool
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder a path is walked from")
	set.StringVar(&name, "name", "", "the file's name on the server (default: the local file's name)")
	set.BoolVar(&parents, "parents", false, "make the missing directories on the path")
	set.BoolVar(&replace, "replace", false, "replace a file of the same name; its old contents are kept as a version")
	set.BoolVar(&rename, "rename", false, "take a free name when the name is in use")
	set.StringVar(&resume, "resume", "", "go on with the upload `id` (upl_...) that this client began")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if replace && rename {
		return fail(stderr, g, exitcode.Usagef("--replace and --rename cannot be used together"))
	}
	onConflict := ""
	switch {
	case replace:
		onConflict = "replace"
	case rename:
		onConflict = "rename"
	}
	piece, explicit, err := tf.pieceSize()
	if err != nil {
		return fail(stderr, g, err)
	}
	if name != "" {
		if err := checkName(name); err != nil {
			return fail(stderr, g, err)
		}
	}
	ctx := context.Background()
	if resume != "" {
		if len(pos) != 0 || folder != "" || parents {
			return fail(stderr, g, exitcode.Usagef("--resume goes on with the upload as it was begun: it takes no file, "+
				"--folder or --parents"))
		}
		if !id.Valid("upl", resume) {
			return fail(stderr, g, exitcode.Usagef("an upload is named by its upl_ id; selfcloud upload ls lists them"))
		}
		return putResume(ctx, g, tf, resume, name, onConflict, piece, explicit, stdout, stderr)
	}

	if len(pos) < 1 || len(pos) > 2 {
		return fail(stderr, g, exitcode.Usagef("put takes a local file, and a directory: a path in --folder, or a "+
			"directory's node id"))
	}
	local, err := filepath.Abs(pos[0])
	if err != nil {
		return fail(stderr, g, exitcode.Usagef("the local file's path cannot be made absolute"))
	}
	fi, err := os.Stat(local)
	switch {
	case err != nil:
		return fail(stderr, g, exitcode.Usagef("cannot read the local file %s", local))
	case fi.IsDir():
		return fail(stderr, g, exitcode.Usagef("put sends one file, and %s is a directory", local))
	case !fi.Mode().IsRegular():
		return fail(stderr, g, exitcode.Usagef("put sends a regular file, and %s is not one", local))
	}
	if name == "" {
		name = filepath.Base(local)
		if checkName(name) != nil {
			return fail(stderr, g, exitcode.Usagef("the local file's name is not one the server takes; give one with --name"))
		}
	}
	if onConflict == "" {
		onConflict = "fail"
	}
	rec := uploadRecord{Local: local, Size: fi.Size(), LocalModified: fi.ModTime(), Name: name, OnConflict: onConflict}
	parentID := ""
	if len(pos) == 2 && !strings.HasPrefix(pos[1], "/") {
		if parentID, err = nodeArg(pos[1]); err != nil {
			return fail(stderr, g, err)
		}
		if parents {
			return fail(stderr, g, exitcode.Usagef("--parents goes with a path; a node id names a directory that exists"))
		}
		if folder != "" {
			if _, err := folderArg(folder); err != nil {
				return fail(stderr, g, err)
			}
		}
	} else {
		fid, err := requireFolder(folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		rec.Folder = fid
		if len(pos) == 2 {
			p, err := remotePath(pos[1])
			if err != nil {
				return fail(stderr, g, err)
			}
			if p != "/" {
				rec.Path = p
			}
		}
	}

	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	lim, err := serverLimits(ctx, c.Client)
	if err != nil {
		return fail(stderr, g, err)
	}
	if piece, err = fitPiece(piece, explicit, lim.MaxChunkSize); err != nil {
		return fail(stderr, g, err)
	}
	if lim.MaxFileSize > 0 && rec.Size > lim.MaxFileSize {
		return fail(stderr, g, exitcode.Usagef("the file is %d bytes, and this server takes files of at most %d bytes",
			rec.Size, lim.MaxFileSize))
	}
	if parentID != "" {
		v, err := statOf(ctx, c.Client, parentID)
		if err != nil {
			return fail(stderr, g, err)
		}
		if v.Kind != "dir" {
			return fail(stderr, g, exitcode.Usagef("the destination is not a directory"))
		}
		if folder != "" && v.FolderID != folder {
			return fail(stderr, g, exitcode.Usagef("the directory is not in the folder --folder names"))
		}
		rec.Folder, rec.Parent = v.FolderID, parentID
	}
	rec.Server, rec.Profile = c.Client.BaseURL(), c.Profile

	f, err := os.Open(local)
	if err != nil {
		return fail(stderr, g, exitcode.Usagef("cannot read the local file %s", local))
	}
	defer f.Close()
	u := &uploader{cl: c.Client, rec: rec, file: f, piece: piece, stall: tf.stall, stderr: stderr}
	nodeID, err := u.create(ctx, parents)
	if err == nil && nodeID == "" {
		nodeID, err = u.send(ctx, 0, false)
	}
	return u.finish(ctx, g, nodeID, nil, err, stdout, stderr)
}

// putResume goes on with an upload this client began: its record names the file, the server and the target. An upload
// whose bytes are all on the server and that waits for a name is committed, under --name, --rename or --replace when
// given.
func putResume(ctx context.Context, g conn.Global, tf transferFlags, uplID, name, onConflict string, piece int64,
	explicit bool, stdout, stderr io.Writer) int {
	rec, err := loadUploadRecord(uplID)
	if errors.Is(err, errNoRecord) {
		return fail(stderr, g, exitcode.NotFoundf("no upload %s is recorded here; selfcloud upload ls lists the ones "+
			"this client began", uplID))
	}
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if c.Client.BaseURL() != rec.Server {
		return fail(stderr, g, exitcode.Usagef("the upload %s was begun on another server; resume it with that "+
			"server's address or profile", uplID))
	}
	fi, err := os.Stat(rec.Local)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != rec.Size || !fi.ModTime().Equal(rec.LocalModified) {
		return fail(stderr, g, exitcode.Usagef("the local file %s is not the one the upload began with (it is missing, "+
			"or its size or time changed); cancel the upload with selfcloud upload cancel %s and start again",
			rec.Local, uplID))
	}
	lim, err := serverLimits(ctx, c.Client)
	if err != nil {
		return fail(stderr, g, err)
	}
	if piece, err = fitPiece(piece, explicit, lim.MaxChunkSize); err != nil {
		return fail(stderr, g, err)
	}
	f, err := os.Open(rec.Local)
	if err != nil {
		return fail(stderr, g, exitcode.Usagef("cannot read the local file %s", rec.Local))
	}
	defer f.Close()
	u := &uploader{cl: c.Client, rec: rec, file: f, piece: piece, stall: tf.stall, stderr: stderr}

	h, err := u.head(ctx)
	switch {
	case err != nil && repeatable(err):
		// The server is not answering now: the piece loop asks again, within its tries.
		nodeID, err := u.send(ctx, rec.Sent, true)
		return u.finish(ctx, g, nodeID, nil, err, stdout, stderr)
	case err != nil:
		return u.finish(ctx, g, "", nil, err, stdout, stderr)
	case h.awaiting:
		body, err := u.commit(ctx, name, onConflict)
		return u.finish(ctx, g, "", body, err, stdout, stderr)
	case name != "" || onConflict != "":
		return fail(stderr, g, exitcode.Usagef("--name, --rename and --replace apply to an upload that waits for a name; "+
			"this one holds %d of %d bytes, so it goes on under its own name", h.offset, h.length))
	}
	nodeID, err := u.send(ctx, h.offset, false)
	return u.finish(ctx, g, nodeID, nil, err, stdout, stderr)
}

// limits are the server's upload limits, from its info document.
type limits struct {
	MaxFileSize  int64 `json:"max_file_size"`
	MaxChunkSize int64 `json:"max_chunk_size"`
}

// serverLimits reads the largest file and the largest piece the server takes (upload.max_file_size and
// upload.max_chunk_size), from GET /api/v1/info, the document info prints.
func serverLimits(ctx context.Context, cl *client.Client) (limits, error) {
	resp, err := cl.Do(ctx, client.Request{Method: http.MethodGet, Path: infoRoute, Accept: "application/json"})
	if err != nil {
		return limits{}, err
	}
	var doc struct {
		Limits limits `json:"limits"`
	}
	if json.Unmarshal(resp.Body, &doc) != nil || doc.Limits.MaxChunkSize <= 0 {
		return limits{}, errors.New("the server's info gives no piece limit (max_chunk_size)")
	}
	return doc.Limits, nil
}

// fitPiece keeps a piece within the server's limit. A size the person chose that is above it is refused; the default
// takes the limit.
func fitPiece(piece int64, explicit bool, limit int64) (int64, error) {
	if piece <= limit {
		return piece, nil
	}
	if explicit {
		return 0, exitcode.Usagef("the piece size is above this server's limit of %d bytes (upload.max_chunk_size); give "+
			"a smaller --piece-size", limit)
	}
	return limit, nil
}

// statOf reads one node.
func statOf(ctx context.Context, cl *client.Client, nodeID string) (nodeView, error) {
	resp, err := cl.Do(ctx, client.Request{Method: http.MethodGet, Path: nodeRoute(nodeID, ""), Accept: "application/json"})
	if err != nil {
		return nodeView{}, err
	}
	return decodeNode(resp.Body)
}

// uploader is one upload in progress.
type uploader struct {
	cl     *client.Client
	rec    uploadRecord
	file   *os.File
	piece  int64
	stall  time.Duration
	stderr io.Writer
	// noted is set once a failed write of the record has been reported, so it is reported once.
	noted bool
}

// create starts the upload (tus creation, with no body). It returns the new node's id when the file is complete at once,
// as an empty file is; otherwise the upload is recorded, so it can be resumed by its id.
func (u *uploader) create(ctx context.Context, parents bool) (string, error) {
	h := tusHeader()
	h.Set("Upload-Length", strconv.FormatInt(u.rec.Size, 10))
	h.Set("Upload-Metadata", u.metadata(parents))
	resp, err := u.cl.Do(ctx, client.Request{Method: http.MethodPost, Path: uploadsRoute, Accept: "application/json",
		Header: h})
	if err != nil {
		return "", err
	}
	uplID, ok := uploadIDOf(resp.Header.Get("Location"))
	if !ok {
		return "", errors.New("the server's answer names no upload")
	}
	u.rec.ID, u.rec.StartedAt = uplID, time.Now()
	u.noteExpiry(resp.Header)
	if nodeID := resp.Header.Get(nodeIDHeader); nodeID != "" {
		if !id.Valid("nod", nodeID) {
			return "", errors.New("the server's answer names a file that is not a node id")
		}
		return nodeID, nil
	}
	if err := saveUploadRecord(u.rec); err != nil {
		// Without its record the upload could not be resumed by its id, so it is ended rather than left on the server.
		_, _ = u.cl.Do(ctx, client.Request{Method: http.MethodDelete, Path: uploadRoute(uplID, ""), Header: tusHeader()})
		u.rec.ID = ""
		return "", fmt.Errorf("the upload's record could not be written, so the upload was ended: %w", err)
	}
	return "", nil
}

// metadata is the Upload-Metadata of the creation: the target the server checks with the API's rules.
func (u *uploader) metadata(parents bool) string {
	pairs := []string{"folder " + b64std(u.rec.Folder), "name " + b64std(u.rec.Name),
		"on_conflict " + b64std(u.rec.OnConflict)}
	if u.rec.Parent != "" {
		pairs = append(pairs, "parent "+b64std(u.rec.Parent))
	}
	if u.rec.Path != "" {
		pairs = append(pairs, "path "+b64std(u.rec.Path))
	}
	if parents {
		pairs = append(pairs, "parents "+b64std("true"))
	}
	return strings.Join(pairs, ",")
}

func b64std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// uploadIDOf takes the upload's id from a Location header. Only the id is used: every request goes to the client's own
// server, whatever host the header names, so the credential never goes to another one.
func uploadIDOf(loc string) (string, bool) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", false
	}
	dir, last := path.Split(u.Path)
	if !strings.HasSuffix(dir, uploadsRoute+"/") || !id.Valid("upl", last) {
		return "", false
	}
	return last, true
}

// headAnswer is what HEAD says of an upload.
type headAnswer struct {
	offset, length int64
	awaiting       bool
}

// head asks how far the upload got. A HEAD answer has no body, so an error is known by its status alone: 410 is the
// upload's expiry and 404 an upload that is not there.
func (u *uploader) head(ctx context.Context) (headAnswer, error) {
	resp, err := u.cl.Do(ctx, client.Request{Method: http.MethodHead, Path: uploadRoute(u.rec.ID, ""), Header: tusHeader()})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Code == "" {
			switch ae.Status {
			case http.StatusGone:
				ae.Code, ae.Message = "upload_expired", "the upload expired"
			case http.StatusNotFound:
				ae.Code, ae.Message = "not_found", "the upload is not on the server"
			}
		}
		return headAnswer{}, err
	}
	off, err1 := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	length, err2 := strconv.ParseInt(resp.Header.Get("Upload-Length"), 10, 64)
	if err1 != nil || err2 != nil || off < 0 || off > length {
		return headAnswer{}, errors.New("the server's answer gives no valid Upload-Offset and Upload-Length")
	}
	u.noteExpiry(resp.Header)
	return headAnswer{offset: off, length: length, awaiting: resp.Header.Get(uploadStateHeader) == awaitingCommit}, nil
}

// send sends the file from off, piece by piece, until the server holds all of it, and returns the new node's id. With
// sync, it first asks the server how far the upload got. After a failure that may pass (a dropped connection, a stall, a
// server error, a piece another request holds, a checksum or an offset the server refused) it asks again (HEAD) and
// goes on from the server's offset, so nothing the server kept is sent twice. It gives up after maxTries failures in a
// row with nothing gained. An expired upload is never retried.
func (u *uploader) send(ctx context.Context, off int64, sync bool) (string, error) {
	tries := 0
	for {
		if sync {
			h, err := u.head(ctx)
			if err != nil {
				if !repeatable(err) {
					return "", err
				}
				if tries++; tries >= maxTries {
					return "", err
				}
				if perr := pause(ctx, tries, err); perr != nil {
					return "", perr
				}
				continue
			}
			if h.length != u.rec.Size {
				return "", fmt.Errorf("the upload on the server is %d bytes long and the file %d; it is not this file's",
					h.length, u.rec.Size)
			}
			if h.awaiting {
				return "", errAwaiting
			}
			off, sync = h.offset, false
			u.recordSent(off)
		}
		if err := u.checkLocal(); err != nil {
			return "", err
		}
		n := min(u.piece, u.rec.Size-off)
		nodeID, next, err := u.patch(ctx, off, n)
		if err == nil {
			if nodeID != "" {
				return nodeID, nil
			}
			if next <= off || next > u.rec.Size {
				return "", fmt.Errorf("the server took the piece at %d and answered the offset %d", off, next)
			}
			off, tries = next, 0
			u.recordSent(off)
			if off == u.rec.Size {
				return "", errors.New("the server holds the whole file but named no file")
			}
			continue
		}
		switch code := apiCode(err); {
		case code == "upload_offset_mismatch", code == "checksum_mismatch", repeatable(err):
			sync = true
		default:
			return "", err
		}
		if tries++; tries >= maxTries {
			return "", err
		}
		if perr := pause(ctx, tries, err); perr != nil {
			return "", perr
		}
	}
}

// patch sends one piece of n bytes at off, with the SHA-256 of its bytes. The bytes are read twice from the file, once to
// hash and once to send, so a piece is never held whole in memory.
func (u *uploader) patch(ctx context.Context, off, n int64) (nodeID string, next int64, err error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(u.file, off, n)); err != nil {
		return "", 0, fmt.Errorf("cannot read the local file: %w", err)
	}
	hdr := tusHeader()
	hdr.Set("Upload-Offset", strconv.FormatInt(off, 10))
	hdr.Set("Upload-Checksum", "sha256 "+base64.StdEncoding.EncodeToString(h.Sum(nil)))
	resp, err := u.cl.Stream(ctx, client.Request{Method: http.MethodPatch, Path: uploadRoute(u.rec.ID, ""), Header: hdr,
		ContentType: pieceType, Upload: io.NewSectionReader(u.file, off, n), UploadLength: n}, u.stall)
	if err != nil {
		return "", 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	u.noteExpiry(resp.Header)
	next, err = strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		return "", 0, errors.New("the server's answer gives no valid Upload-Offset")
	}
	if nodeID = resp.Header.Get(nodeIDHeader); nodeID != "" && !id.Valid("nod", nodeID) {
		return "", 0, errors.New("the server's answer names a file that is not a node id")
	}
	return nodeID, next, nil
}

// checkLocal refuses to go on when the local file is not the one the upload began with: its pieces would not belong
// together.
func (u *uploader) checkLocal() error {
	fi, err := os.Stat(u.rec.Local)
	if err == nil && fi.Size() == u.rec.Size && fi.ModTime().Equal(u.rec.LocalModified) {
		return nil
	}
	return &exitcode.Error{Exit: exitcode.Failed, Name: "local_changed", Message: fmt.Sprintf("the local file %s "+
		"changed during the upload, so its pieces would not belong together; cancel the upload with selfcloud upload "+
		"cancel %s and start again", u.rec.Local, u.rec.ID)}
}

// recordSent keeps the offset the server acknowledged in the upload's record. A record that cannot be written is
// reported once; the upload goes on.
func (u *uploader) recordSent(off int64) {
	u.rec.Sent = off
	if err := saveUploadRecord(u.rec); err != nil && !u.noted {
		u.noted = true
		fmt.Fprintf(u.stderr, "selfcloud: note: the upload's record could not be updated: %v\n", err)
	}
}

// noteExpiry keeps the end the server last gave the upload.
func (u *uploader) noteExpiry(h http.Header) {
	if t, err := http.ParseTime(h.Get("Upload-Expires")); err == nil {
		u.rec.ExpiresAt = t
	}
}

// commit gives an upload that waits for a name its name, and returns the new node's document.
func (u *uploader) commit(ctx context.Context, name, onConflict string) ([]byte, error) {
	doc := map[string]string{}
	if name != "" {
		doc["name"] = name
	}
	if onConflict != "" {
		doc["on_conflict"] = onConflict
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	resp, err := u.cl.Do(ctx, client.Request{Method: http.MethodPost, Path: uploadRoute(u.rec.ID, "/commit"),
		Accept: "application/json", ContentType: "application/json", Body: b})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// isAwaiting reports whether err says the upload waits for a name.
func isAwaiting(err error) bool {
	if errors.Is(err, errAwaiting) {
		return true
	}
	var ae *client.APIError
	return errors.As(err, &ae) && ae.Details["state"] == awaitingCommit
}

// finish reports the upload's end. A file made by the last piece is read and printed; a commit's answer is printed as
// it is. An upload that failed is explained: expired (exit 6, never retried), gone, waiting for a name, or kept for a
// resume.
func (u *uploader) finish(ctx context.Context, g conn.Global, nodeID string, doc []byte, err error,
	stdout, stderr io.Writer) int {
	if err != nil {
		return fail(stderr, g, u.explain(err))
	}
	if u.rec.ID != "" {
		if ferr := forgetUpload(u.rec.ID); ferr != nil {
			fmt.Fprintf(stderr, "selfcloud: note: the finished upload's record could not be removed: %v\n", ferr)
		}
	}
	if doc == nil {
		resp, serr := u.cl.Do(ctx, client.Request{Method: http.MethodGet, Path: nodeRoute(nodeID, ""),
			Accept: "application/json"})
		if serr != nil {
			fmt.Fprintf(stderr, "selfcloud: note: the file was made, but it could not be read back: %s\n", serr)
			if g.JSON {
				_ = output.JSON(stdout, map[string]string{"id": nodeID})
			} else {
				_ = output.KeyValues(stdout, []output.KV{{Key: "id", Value: nodeID}})
			}
			return exitcode.OK
		}
		doc = resp.Body
	}
	if err := printNode(stdout, doc, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// explain turns a failed upload into the error the person reads.
func (u *uploader) explain(err error) error {
	if u.rec.ID == "" {
		return err
	}
	switch {
	case apiCode(err) == "upload_expired":
		_ = forgetUpload(u.rec.ID)
		return &exitcode.Error{Exit: exitcode.UploadExpired, Name: "upload_expired", Message: fmt.Sprintf("the upload "+
			"of %s expired on the server (%s), which dropped what it held; the file must be sent again from the start: "+
			"run selfcloud put again", u.rec.Local, u.rec.ID)}
	case apiCode(err) == "not_found":
		_ = forgetUpload(u.rec.ID)
		return &exitcode.Error{Exit: exitcode.NotFound, Name: "not_found", Message: fmt.Sprintf("the upload %s of %s "+
			"is no longer on the server; if its last piece arrived, the file was made, so look for %s with selfcloud "+
			"ls; otherwise send it again", u.rec.ID, u.rec.Local, u.rec.Name)}
	case isAwaiting(err):
		u.recordSent(u.rec.Size)
		return &exitcode.Error{Exit: exitcode.Refused, Name: "conflict", Message: fmt.Sprintf("the name %s is taken in "+
			"that directory now; every byte of %s is on the server and the upload waits for a name: run selfcloud put "+
			"--resume %s with --name NEW, --rename or --replace", u.rec.Name, u.rec.Local, u.rec.ID)}
	}
	var ee *exitcode.Error
	if errors.As(err, &ee) && ee.Name == "local_changed" {
		return err
	}
	return withHint(err, fmt.Sprintf("the upload is kept: run selfcloud put --resume %s to go on", u.rec.ID))
}
