// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"encoding/hex"
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
	"github.com/shukiv/selfcloud/internal/names"
)

// maxRestarts is how many times one download starts again because the file changed on the server, before it gives up.
const maxRestarts = 3

// errRangeIgnored is a server, or a proxy in front of it, that answered a range with the whole file of the same version.
// Running get again meets the same answer, so it is not told to.
var errRangeIgnored = errors.New("the server, or a proxy in front of it, sent the whole file instead of a range; get " +
	"needs the Range and If-Range headers to reach the server")

// Get is `selfcloud get FILE [DEST]`: a resumable download in ranges (design-cli 3.9). FILE is a node id, a path in
// --folder, or a share link, which needs no sign-in (3.5). DEST is a directory, where the file takes the server's name,
// or a new file's path; the default is the current directory.
func Get(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("get")
	var g conn.Global
	g.Register(set)
	var tf transferFlags
	tf.register(set)
	var folder, linkFile string
	var overwrite, passwordStdin bool
	set.StringVar(&linkFile, "link-file", "", "read a share link from this regular `file` instead of the command line, "+
		"where its secret would show in the process list; the argument is then only the destination")
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder a path is walked from")
	set.BoolVar(&overwrite, "overwrite", false, "replace a regular file at the destination, once the download is "+
		"complete and checked; a part file this download cannot continue is replaced by a new one")
	set.BoolVar(&passwordStdin, "password-stdin", false, "read a share link's password from standard input, which must "+
		"not be a terminal")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if linkFile != "" {
		if len(pos) > 1 {
			return fail(stderr, g, exitcode.Usagef("with --link-file, get takes only a destination: the link is in the file"))
		}
	} else if len(pos) < 1 || len(pos) > 2 {
		return fail(stderr, g, exitcode.Usagef("get takes a file (its node id, a path in --folder, or a share link) "+
			"and a destination"))
	}
	piece, _, err := tf.pieceSize()
	if err != nil {
		return fail(stderr, g, err)
	}
	opts := dlOptions{piece: piece, stall: tf.stall, overwrite: overwrite}
	if linkFile != "" || pos[0] == "-" {
		// The link is read from a file, or from standard input, so its secret is never in argv.
		source, rest := linkFile, pos
		if linkFile == "" {
			if passwordStdin {
				return fail(stderr, g, exitcode.Usagef("get - and --password-stdin both read standard input; give the "+
					"link with --link-file instead"))
			}
			source, rest = "-", pos[1:]
		}
		link, err := readLink(source)
		if err != nil {
			return fail(stderr, g, err)
		}
		dest := ""
		if len(rest) == 1 {
			dest = rest[0]
		}
		return getShared(g, link, dest, folder, passwordStdin, opts, stdout, stderr)
	}
	dest := ""
	if len(pos) == 2 {
		dest = pos[1]
	}
	if strings.Contains(pos[0], "://") {
		return getShared(g, pos[0], dest, folder, passwordStdin, opts, stdout, stderr)
	}
	if passwordStdin {
		return fail(stderr, g, exitcode.Usagef("--password-stdin goes with a share link"))
	}
	nodeID, remote := "", ""
	if strings.HasPrefix(pos[0], "/") {
		fid, err := requireFolder(folder)
		if err != nil {
			return fail(stderr, g, err)
		}
		if remote, err = remotePath(pos[0]); err != nil {
			return fail(stderr, g, err)
		}
		if remote == "/" {
			return fail(stderr, g, exitcode.Usagef("get takes a file, and / is the folder's root"))
		}
		folder = fid
	} else {
		if folder != "" {
			return fail(stderr, g, exitcode.Usagef("--folder goes with a path; a node id names its own folder"))
		}
		if nodeID, err = nodeArg(pos[0]); err != nil {
			return fail(stderr, g, err)
		}
	}
	dst, err := openDestination(dest, overwrite)
	if err != nil {
		return fail(stderr, g, err)
	}
	defer dst.close()
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if remote != "" {
		if nodeID, err = findByPath(c, folder, remote); err != nil {
			return fail(stderr, g, err)
		}
	}
	got, err := download(context.Background(), signedIn{c.Client}, nodeID, dst, opts, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	return printGot(stdout, stderr, g, got)
}

// dlOptions are the choices of one download.
type dlOptions struct {
	piece     int64
	stall     time.Duration
	overwrite bool
}

// localDest is the directory a download writes in, opened as a root, and the name the person chose, or "".
type localDest struct {
	root *os.Root
	dir  string // absolute
	name string
}

func (d localDest) close() { _ = d.root.Close() }

// openDestination opens the destination's directory as a root, before any request. A name the person chose is checked
// with the same rules as a server's, and refused at once when it is taken.
func openDestination(arg string, overwrite bool) (localDest, error) {
	t := resolveTarget(arg)
	dir, err := filepath.Abs(t.dir)
	if err != nil {
		return localDest{}, exitcode.Usagef("the destination's path cannot be made absolute")
	}
	name := ""
	if t.name != "" {
		if name, err = localName(t.name); err != nil {
			return localDest{}, err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return localDest{}, exitcode.Usagef("the destination directory %s cannot be opened", dir)
	}
	d := localDest{root: root, dir: dir, name: name}
	if name != "" {
		if err := checkFinal(root, name, overwrite); err != nil {
			d.close()
			return localDest{}, err
		}
	}
	return d, nil
}

// findByPath finds the file at a path in a folder: it lists the directory above it and matches the name as the API
// does, by its key, so the case and the Unicode form need not match.
func findByPath(c *conn.Conn, folderID, p string) (string, error) {
	dir, base := path.Split(p)
	if dir != "/" {
		dir = strings.TrimSuffix(dir, "/")
	}
	_, key, err := names.Clean(base)
	if err != nil {
		return "", exitcode.Usagef("a name in the path is not valid: %s", err)
	}
	items, _, err := listPages(c, request{method: http.MethodGet, path: folderRoute(folderID, "/children"),
		query: url.Values{"path": {dir}}}, true)
	if err != nil {
		return "", err
	}
	for _, raw := range items {
		v, err := decodeNode(raw)
		if err != nil {
			return "", err
		}
		if _, k, err := names.Clean(v.Name); err == nil && k == key {
			return v.ID, nil
		}
	}
	return "", exitcode.NotFoundf("the path names nothing in the folder")
}

// source is where a download's requests go: the signed-in client, or a share link's visitor.
type source interface {
	// client is the client for the next request, with a credential that is still good.
	client(ctx context.Context) (*client.Client, error)
	// reopen answers a refused credential. A visitor opens the link again and the request may be made again; a
	// signed-in client cannot.
	reopen(ctx context.Context) bool
	// server is the address the requests go to, for the download's record.
	server() string
}

// signedIn is a download with the profile's or the flags' credential.
type signedIn struct{ cl *client.Client }

func (s signedIn) client(context.Context) (*client.Client, error) { return s.cl, nil }
func (s signedIn) reopen(context.Context) bool                    { return false }
func (s signedIn) server() string                                 { return s.cl.BaseURL() }

// gotFile is what get prints: where the file is, its size and SHA-256, and which check was made.
type gotFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Checked string `json:"checked"`
}

// version is the file a download reads: its version (ETag), its size and its SHA-256, when the server gives one.
type version struct {
	etag string
	size int64
	sha  string
}

// fileVersion reads a node and refuses what is not a file with a valid version.
func fileVersion(ctx context.Context, src source, nodeID string) (nodeView, version, error) {
	cl, err := src.client(ctx)
	if err != nil {
		return nodeView{}, version{}, err
	}
	v, err := statOf(ctx, cl, nodeID)
	if err != nil {
		return nodeView{}, version{}, err
	}
	switch {
	case v.Kind == "dir":
		return v, version{}, exitcode.Usagef("the node is a directory; get takes a file")
	case v.Kind != "file":
		return v, version{}, exitcode.Usagef("get takes a file, and the node is not one")
	case !validETag(v.ETag) || v.Size < 0:
		return v, version{}, errors.New("the server gives the file no valid version (etag) or size")
	}
	ver := version{etag: v.ETag, size: v.Size}
	if v.SHA256 != "" {
		b, err := hex.DecodeString(v.SHA256)
		if err != nil || len(b) != 32 {
			return v, version{}, errors.New("the server's SHA-256 of the file is not valid")
		}
		ver.sha = hex.EncodeToString(b)
	}
	return v, ver, nil
}

// download reads the node into the destination. The bytes go to NAME.part, made with O_EXCL, and the part file takes
// the name only when it is complete and checked. Each range asks with If-Range, so a file that changes is sent whole and
// the download starts again on it, never mixing two versions. A part file that this client began for the same file and
// version is continued from its size.
func download(ctx context.Context, src source, nodeID string, dst localDest, o dlOptions,
	stderr io.Writer) (gotFile, error) {
	v, ver, err := fileVersion(ctx, src, nodeID)
	if err != nil {
		return gotFile{}, err
	}
	name := dst.name
	if name == "" {
		if name, err = serverName(v.Name); err != nil {
			return gotFile{}, err
		}
		if err := checkFinal(dst.root, name, o.overwrite); err != nil {
			return gotFile{}, err
		}
	}
	d := &downloader{src: src, nodeID: nodeID, ver: ver, o: o, stderr: stderr,
		partPath: filepath.Join(dst.dir, name+partSuffix)}
	pf, err := d.startPart(dst.root, name)
	if err != nil {
		return gotFile{}, err
	}
	d.pf = pf
	if err := d.fetch(ctx); err != nil {
		var lw *localWriteError
		if errors.As(err, &lw) {
			pf.close()
			return gotFile{}, err
		}
		pf.close()
		if errors.Is(err, errRangeIgnored) {
			return gotFile{}, err
		}
		return gotFile{}, withHint(err, fmt.Sprintf("the part file %s is kept: run selfcloud get again to go on",
			d.partPath))
	}
	sum := hex.EncodeToString(pf.sum.Sum(nil))
	checked := "size"
	if d.ver.sha != "" {
		if sum != d.ver.sha {
			pf.discard()
			_ = forgetDownload(d.partPath)
			return gotFile{}, &exitcode.Error{Exit: exitcode.CheckFailed, Name: "checksum_mismatch", Message: "the " +
				"downloaded bytes do not match the server's SHA-256 of the file, so nothing was kept; run selfcloud " +
				"get again"}
		}
		checked = "sha256"
	} else {
		fmt.Fprintln(stderr, "selfcloud: note: the server gives no SHA-256 of this file, so only its size was checked")
	}
	if err := pf.finish(o.overwrite); err != nil {
		return gotFile{}, err
	}
	_ = forgetDownload(d.partPath)
	return gotFile{Path: filepath.Join(dst.dir, name), Size: pf.size, SHA256: sum, Checked: checked}, nil
}

// downloader is one download in progress.
type downloader struct {
	src      source
	nodeID   string
	ver      version
	o        dlOptions
	stderr   io.Writer
	partPath string
	pf       *partFile
	noted    bool
}

// startPart makes the part file, or continues the one this client began for the same file. A part file that is not
// this client's for the same file, that another user owns, or that has another name (a hard link) is not continued: it
// is refused, or, with --overwrite, removed and made anew, so its bytes and its other names are left as they were. One
// that is not a regular file is always refused.
func (d *downloader) startPart(root *os.Root, name string) (*partFile, error) {
	part := name + partSuffix
	fi, err := root.Lstat(part)
	if errors.Is(err, os.ErrNotExist) {
		return d.freshPart(newPart(root, name))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the destination: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, exitcode.Usagef("%s is at the destination and is not a regular file, so it is not used", part)
	}
	rec, rerr := loadDownloadRecord(d.partPath)
	ours := rerr == nil && rec.Server == d.src.server() && rec.Node == d.nodeID
	if problem := partProblem(fi); !ours || problem != "" {
		switch {
		case d.o.overwrite:
			return d.freshPart(replacePart(root, name))
		case problem != "":
			return nil, exitcode.Usagef("%s is at the destination and %s; move it away, or pass --overwrite to "+
				"replace it with a new part file", part, problem)
		}
		return nil, exitcode.Usagef("%s is at the destination, and no download of this file by this client began "+
			"it; move it away, or pass --overwrite to replace it with a new part file", part)
	}
	pf, err := oldPart(root, name, true)
	if err != nil {
		return nil, err
	}
	d.pf = pf
	switch {
	case rec.ETag != d.ver.etag:
		fmt.Fprintln(d.stderr, "selfcloud: the file changed on the server since the part file was begun; starting again "+
			"on its new version")
	case pf.size > d.ver.size:
		fmt.Fprintln(d.stderr, "selfcloud: the part file is longer than the file; starting again")
	default:
		fmt.Fprintf(d.stderr, "selfcloud: going on from byte %d of %d\n", pf.size, d.ver.size)
		return pf, nil
	}
	if err := pf.reset(); err != nil {
		pf.close()
		return nil, err
	}
	d.saveRecord()
	return pf, nil
}

// freshPart takes a new part file and records it.
func (d *downloader) freshPart(pf *partFile, err error) (*partFile, error) {
	if err != nil {
		return nil, err
	}
	d.pf = pf
	d.saveRecord()
	return pf, nil
}

// saveRecord records the part file, so a later get continues it. A record that cannot be written is reported once; the
// download goes on, and a later get treats the part file as one it did not begin.
func (d *downloader) saveRecord() {
	err := saveDownloadRecord(downloadRecord{Part: d.partPath, Server: d.src.server(), Node: d.nodeID,
		ETag: d.ver.etag, Size: d.ver.size, StartedAt: time.Now()})
	if err != nil && !d.noted {
		d.noted = true
		fmt.Fprintf(d.stderr, "selfcloud: note: the download's record could not be written: %v\n", err)
	}
}

// fetch fills the part file, range by range, from its size. After a failure that may pass it asks again from the part
// file's new size, and gives up after maxTries failures in a row with no byte gained. A whole file in answer to a range
// means the file changed: the version is read again and the part file starts again.
func (d *downloader) fetch(ctx context.Context) error {
	tries, restarts, restat := 0, 0, false
	for {
		var err error
		if restat {
			var ver version
			if _, ver, err = fileVersion(ctx, d.src, d.nodeID); err == nil {
				d.ver, restat = ver, false
				if err = d.pf.reset(); err != nil {
					return err
				}
				d.saveRecord()
				continue
			}
		} else {
			if d.pf.size >= d.ver.size {
				return nil
			}
			var changed bool
			before := d.pf.size
			changed, err = d.fetchRange(ctx)
			if d.pf.size > before {
				tries = 0
			}
			if changed {
				// A failed read of the new version below is a failure like any other; only a change counts here.
				if restarts++; restarts > maxRestarts {
					return errors.New("the file kept changing on the server during the download; try again later")
				}
				fmt.Fprintln(d.stderr, "selfcloud: the file changed on the server during the download; starting again "+
					"on its new version")
				restat = true
				continue
			}
			if err == nil {
				continue
			}
		}
		var lw *localWriteError
		if errors.As(err, &lw) {
			return err
		}
		var ae *client.APIError
		reopened := errors.As(err, &ae) && ae.Status == http.StatusUnauthorized && d.src.reopen(ctx)
		if !reopened && !repeatable(err) {
			return err
		}
		if tries++; tries >= maxTries {
			return err
		}
		if !reopened {
			if perr := pause(ctx, tries, err); perr != nil {
				return perr
			}
		}
	}
}

// fetchRange asks for the next range of the file, with If-Range, and appends what comes to the part file. changed is
// true when the server sent the whole file instead, or a range of another version: the file changed.
func (d *downloader) fetchRange(ctx context.Context) (changed bool, err error) {
	cl, err := d.src.client(ctx)
	if err != nil {
		return false, err
	}
	start := d.pf.size
	end := min(start+d.o.piece, d.ver.size) - 1
	h := http.Header{}
	h.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	h.Set("If-Range", d.ver.etag)
	resp, err := cl.Stream(ctx, client.Request{Method: http.MethodGet, Path: nodeRoute(d.nodeID, "/content"),
		Header: h}, d.o.stall)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.Status {
	case http.StatusPartialContent:
	case http.StatusOK:
		// The whole file of the version asked for is a range that was ignored, not a change, and starting again would
		// meet the same answer.
		if resp.Header.Get("ETag") == d.ver.etag {
			return false, errRangeIgnored
		}
		return true, nil
	default:
		return false, fmt.Errorf("the server answered %d to a range", resp.Status)
	}
	if et := resp.Header.Get("ETag"); et != "" && et != d.ver.etag {
		return true, nil
	}
	if !contentRangeIs(resp.Header.Get("Content-Range"), start, end, d.ver.size) {
		return false, errors.New("the server sent another range than the one asked for")
	}
	_, err = d.pf.copyRange(resp.Body, end-start+1)
	return false, err
}

// contentRangeIs reports whether a Content-Range header is exactly "bytes start-end/size".
func contentRangeIs(h string, start, end, size int64) bool {
	return h == "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10)
}

// printGot writes where the file went and how it was checked.
func printGot(stdout, stderr io.Writer, g conn.Global, got gotFile) int {
	var err error
	if g.JSON {
		err = output.JSON(stdout, got)
	} else {
		err = output.KeyValues(stdout, []output.KV{
			{Key: "path", Value: got.Path},
			{Key: "size", Value: strconv.FormatInt(got.Size, 10)},
			{Key: "sha256", Value: got.SHA256},
			{Key: "checked", Value: got.Checked},
		})
	}
	if err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}
