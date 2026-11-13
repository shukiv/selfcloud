// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// The derived commands (design-cli.md section 8): thumbnail waits for the server's preview of a node and writes it,
// and export asks the server to convert a file to another format, waits for the conversion, and downloads it. Both
// waits follow the server's Retry-After, and neither command asks the server for the same thing twice.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
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
	// thumbWaitTimeout is how long thumbnail follows a thumbnail that is still being made.
	thumbWaitTimeout = time.Minute
	// exportWaitTimeout is how long export follows a conversion that is still being made.
	exportWaitTimeout = 10 * time.Minute
	// derivedRetryMax is the longest wait between two polls.
	derivedRetryMax = 30 * time.Second
	// derivedRetryDefault is the wait when the server gives no usable Retry-After.
	derivedRetryDefault = 2 * time.Second
)

// thumbSleep waits before the next thumbnail poll. Tests make it return at once.
var thumbSleep = derivedSleep

// exportSleep waits before the next export poll. Tests make it return at once.
var exportSleep = derivedSleep

// derivedSleep waits d, or returns the context's error when it ends first or d is not positive.
func derivedSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// derivedRetryAfter reads a Retry-After header as a wait of one to thirty seconds, or the default when it cannot be
// read. The server's shortest wait is a second, which keeps a pending answer from spinning.
func derivedRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if n, err := strconv.Atoi(h); err == nil {
		if n < 1 {
			return time.Second
		}
		return min(time.Duration(n)*time.Second, derivedRetryMax)
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < time.Second {
			return time.Second
		}
		return min(d, derivedRetryMax)
	}
	return derivedRetryDefault
}

// derivedErrorWait is the wait a refusal asks for, or the default.
func derivedErrorWait(err error) time.Duration {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return min(ae.RetryAfter, derivedRetryMax)
	}
	return derivedRetryDefault
}

// thumbRoute is the route of a node's thumbnail.
func thumbRoute(nodeID string) string { return nodeRoute(nodeID, "/thumbnail") }

// Thumbnail is `selfcloud thumbnail NODE DEST`: writes the server's preview of a node. It follows a pending preview
// until it is ready or --wait-timeout ends. DEST is a file, a directory that takes thumbnail.jpg, or - for standard
// output.
func Thumbnail(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("thumbnail")
	var g conn.Global
	g.Register(set)
	var size int
	var waitTimeout time.Duration
	var force bool
	set.IntVar(&size, "size", 0, "the `pixels` of the preview's longest side; the server's smallest when not given")
	set.DurationVar(&waitTimeout, "wait-timeout", thumbWaitTimeout, "give up after this long; the server goes on "+
		"making the thumbnail")
	set.BoolVar(&force, "force", false, "replace a regular file at the destination")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("thumbnail takes a node and a destination"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	given := false
	set.Visit(func(f *flag.Flag) { given = given || f.Name == "size" })
	if given && size <= 0 {
		return fail(stderr, g, exitcode.Usagef("--size must be longer than zero"))
	}
	if waitTimeout <= 0 {
		return fail(stderr, g, exitcode.Usagef("--wait-timeout must be longer than zero"))
	}
	dest := pos[1]
	if dest == "-" {
		if f, ok := stdout.(*os.File); ok && IsTerminal(f) {
			return fail(stderr, g, exitcode.Usagef("the thumbnail would be written to a terminal; give a destination "+
				"file, or redirect standard output"))
		}
		c, err := connect(g, stderr)
		if err != nil {
			return fail(stderr, g, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		body, err := thumbBody(ctx, c.Client, nodeID, size, given, waitTimeout)
		if err != nil {
			return fail(stderr, g, err)
		}
		n, err := io.Copy(stdout, body)
		cerr := body.Close()
		if err != nil {
			return fail(stderr, g, fmt.Errorf("cannot write the thumbnail: %w", err))
		}
		if cerr != nil {
			return fail(stderr, g, fmt.Errorf("cannot finish the thumbnail: %w", cerr))
		}
		return thumbReport(stderr, stderr, g, "-", n)
	}
	dst, err := openDestination(dest, force)
	if err != nil {
		return fail(stderr, g, err)
	}
	defer dst.close()
	if dst.name == "" {
		name := "thumbnail.jpg"
		if err := checkFinal(dst.root, name, force); err != nil {
			return fail(stderr, g, err)
		}
		dst.name = name
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	body, err := thumbBody(ctx, c.Client, nodeID, size, given, waitTimeout)
	if err != nil {
		return fail(stderr, g, err)
	}
	n, err := thumbWrite(dst, body, force)
	cerr := body.Close()
	if err != nil {
		return fail(stderr, g, err)
	}
	if cerr != nil {
		return fail(stderr, g, fmt.Errorf("cannot finish the thumbnail: %w", cerr))
	}
	return thumbReport(stdout, stderr, g, dst.dir+"/"+dst.name, n)
}

// thumbBody asks for the thumbnail, follows a pending answer, and returns the ready body. The caller closes it. A
// signed link, which the command line does not follow, and a wait that ends are reported here.
func thumbBody(ctx context.Context, cl *client.Client, nodeID string, size int, sizeGiven bool,
	limit time.Duration) (io.ReadCloser, error) {
	for {
		if ctx.Err() != nil {
			return nil, thumbTimeout()
		}
		r := client.Request{Method: http.MethodGet, Path: thumbRoute(nodeID)}
		if sizeGiven {
			r.Query = url.Values{"size": {strconv.Itoa(size)}}
		}
		resp, err := cl.Stream(ctx, r, probeStall)
		if err != nil {
			if ctx.Err() != nil {
				return nil, thumbTimeout()
			}
			switch apiStatus(err) {
			case http.StatusSeeOther:
				return nil, thumbSignedLink()
			case http.StatusTooManyRequests:
				if serr := thumbSleep(ctx, derivedErrorWait(err)); serr != nil {
					return nil, thumbTimeout()
				}
				continue
			}
			return nil, err
		}
		switch resp.Status {
		case http.StatusOK:
			return resp.Body, nil
		case http.StatusAccepted:
			d := derivedRetryAfter(resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
			if err := thumbSleep(ctx, d); err != nil {
				return nil, thumbTimeout()
			}
		default:
			_ = resp.Body.Close()
			return nil, fmt.Errorf("the server answered %d to the thumbnail request", resp.Status)
		}
	}
}

// thumbSignedLink is the error of a server that hands thumbnails out through signed links.
func thumbSignedLink() error {
	return &exitcode.Error{Exit: exitcode.Failed, Name: "signed_link", Message: "this server hands thumbnails out " +
		"through signed links, which the command line does not follow"}
}

// thumbTimeout is the error of a wait that ran out.
func thumbTimeout() error {
	return &exitcode.Error{Exit: exitcode.Failed, Name: "timeout", Message: "the thumbnail is not ready yet"}
}

// thumbWrite writes the thumbnail to the destination. Without --force the name is made with O_EXCL, so a file that
// appears meanwhile is kept; with it, the bytes go to a temporary name that replaces a regular file only after it is
// complete and checked.
func thumbWrite(dst localDest, body io.Reader, force bool) (int64, error) {
	if !force {
		f, err := dst.root.OpenFile(dst.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, partMode)
		if err != nil {
			return 0, fmt.Errorf("cannot write the thumbnail: %w", err)
		}
		n, err := thumbCopy(f, body)
		if err == nil {
			err = f.Chmod(newFileMode())
		}
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = dst.root.Remove(dst.name)
			return n, fmt.Errorf("cannot write the thumbnail: %w", err)
		}
		return n, nil
	}
	tmp, err := thumbTempName()
	if err != nil {
		return 0, err
	}
	f, err := dst.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, partMode)
	if err != nil {
		return 0, fmt.Errorf("cannot write the thumbnail: %w", err)
	}
	n, err := thumbCopy(f, body)
	if err == nil {
		err = f.Chmod(newFileMode())
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = checkFinal(dst.root, dst.name, true)
	}
	if err == nil {
		err = dst.root.Rename(tmp, dst.name)
	}
	if err != nil {
		_ = dst.root.Remove(tmp)
		return n, fmt.Errorf("cannot write the thumbnail: %w", err)
	}
	return n, nil
}

// thumbCopy copies the answer to the file.
func thumbCopy(f *os.File, body io.Reader) (int64, error) {
	n, err := io.Copy(f, body)
	if err != nil {
		return n, err
	}
	return n, nil
}

// thumbTempName is a name beside the destination for a thumbnail written with --force.
func thumbTempName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot name a temporary file: %w", err)
	}
	return ".thumbnail-" + hex.EncodeToString(b) + ".tmp", nil
}

// thumbReport writes where the thumbnail went and its size. A thumbnail on standard output keeps the lines on standard
// error, so the image is not mixed with text.
func thumbReport(stdout, stderr io.Writer, g conn.Global, path string, size int64) int {
	kvs := []output.KV{{Key: "saved", Value: path}, {Key: "size", Value: strconv.FormatInt(size, 10)}}
	var err error
	if g.JSON {
		err = output.JSON(stdout, map[string]any{"saved": path, "size": size})
	} else {
		err = output.KeyValues(stdout, kvs)
	}
	if err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// exportFormats are the formats the export route accepts.
var exportFormats = map[string]bool{"docx": true, "odp": true, "ods": true, "odt": true, "pdf": true,
	"pptx": true, "xlsx": true}

// exportRoute is the route of one export.
func exportRoute(nodeID, export string) string { return nodeRoute(nodeID, "/exports/"+export) }

// exportInfo is what a probe of an export says: whether it is ready, its version, size, SHA-256 when the ETag is one,
// the name the server gives it, and the wait a pending answer asks for.
type exportInfo struct {
	pending bool
	ranged  bool
	etag    string
	sha     string
	size    int64
	name    string
	retry   time.Duration
}

// Export is `selfcloud export NODE --format FORMAT [DEST]`: asks the server to convert a file, follows the conversion,
// and downloads it as get downloads a file. DEST is a directory, where the export takes the server's name, or a new
// file's path.
func Export(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("export")
	var g conn.Global
	g.Register(set)
	var format string
	var waitTimeout time.Duration
	var force bool
	set.StringVar(&format, "format", "", "the `format` to convert to: docx, odp, ods, odt, pdf, pptx or xlsx")
	set.DurationVar(&waitTimeout, "wait-timeout", exportWaitTimeout, "give up after this long; the server goes on "+
		"making the export")
	set.BoolVar(&force, "force", false, "replace a regular file at the destination")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fail(stderr, g, exitcode.Usagef("export takes a node and, at most, a destination"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	if !exportFormats[format] {
		return fail(stderr, g, exitcode.Usagef("--format takes one of docx, odp, ods, odt, pdf, pptx, xlsx"))
	}
	if waitTimeout <= 0 {
		return fail(stderr, g, exitcode.Usagef("--wait-timeout must be longer than zero"))
	}
	dest := ""
	if len(pos) == 2 {
		dest = pos[1]
	}
	dst, err := openDestination(dest, force)
	if err != nil {
		return fail(stderr, g, err)
	}
	defer dst.close()
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	r, err := jsonRequest(http.MethodPost, nodeRoute(nodeID, "/exports"), map[string]string{"format": format})
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, r)
	if err != nil {
		return fail(stderr, g, exportRefusal(err))
	}
	var job struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(resp.Body, &job) != nil || !id.Valid("drv", job.ID) {
		return fail(stderr, g, errors.New("the server's answer names no export"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	info, err := waitForExport(ctx, c.Client, nodeID, job.ID, waitTimeout)
	if err != nil {
		return fail(stderr, g, err)
	}
	name := info.name
	if name == "" {
		name = nodeID + "." + format
	}
	subj := subject{key: nodeID, route: exportRoute(nodeID, job.ID), name: name, verb: "export",
		ver: version{etag: info.etag, size: info.size, sha: info.sha},
		restat: func(ctx context.Context) (version, error) {
			again, err := readyExport(ctx, c.Client, nodeID, job.ID)
			return version{etag: again.etag, size: again.size, sha: again.sha}, err
		}}
	got, err := downloadSubject(ctx, signedIn{c.Client}, subj, dst, dlOptions{piece: defaultPieceSize,
		stall: defaultStall, overwrite: force}, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	return exportReport(stdout, g, got.Path, got.Size)
}

// exportProbe asks for the export's first byte, so the answer carries its state and, when it is ready, its version.
// 202 is a conversion still being made; 206 is a ready one. A refusal comes back as the server's error.
func exportProbe(ctx context.Context, cl *client.Client, nodeID, export string) (exportInfo, error) {
	h := http.Header{}
	h.Set("Range", "bytes=0-0")
	resp, err := cl.Stream(ctx, client.Request{Method: http.MethodGet, Path: exportRoute(nodeID, export), Header: h},
		probeStall)
	if err != nil {
		return exportInfo{}, err
	}
	defer resp.Body.Close()
	var info exportInfo
	switch resp.Status {
	case http.StatusAccepted:
		info.pending = true
		info.retry = derivedRetryAfter(resp.Header.Get("Retry-After"))
		return info, nil
	case http.StatusPartialContent:
		info.ranged = true
	case http.StatusOK:
	default:
		return exportInfo{}, fmt.Errorf("the server answered %d to a range of the export", resp.Status)
	}
	info.etag = resp.Header.Get("ETag")
	if !validETag(info.etag) {
		return exportInfo{}, errors.New("the server gives the export no valid version (etag)")
	}
	if b, err := hex.DecodeString(strings.Trim(info.etag, `"`)); err == nil && len(b) == 32 {
		info.sha = hex.EncodeToString(b)
	}
	sizeText := resp.Header.Get("Content-Length")
	if info.ranged {
		var ok bool
		if sizeText, ok = strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes 0-0/"); !ok {
			sizeText = ""
		}
	}
	if info.size, err = strconv.ParseInt(sizeText, 10, 64); err != nil || info.size < 1 {
		return exportInfo{}, errors.New("the server gives the export no valid size")
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		info.name = params["filename"]
	}
	return info, nil
}

// waitForExport polls the export until it is ready. A 404 after the request was accepted is reported as an export that
// is gone; a refusal that may pass is polled again, with the server's Retry-After.
func waitForExport(ctx context.Context, cl *client.Client, nodeID, export string, limit time.Duration) (exportInfo, error) {
	failures := 0
	for {
		info, err := exportProbe(ctx, cl, nodeID, export)
		switch {
		case ctx.Err() != nil:
			return exportInfo{}, exportTimeout()
		case err == nil && !info.pending:
			return info, nil
		case err == nil:
			failures = 0
		case apiStatus(err) == http.StatusNotFound:
			return exportInfo{}, exportGone(export, err)
		case apiStatus(err) == http.StatusUnsupportedMediaType && exportReason(err) == "failed":
			return exportInfo{}, exportRefusal(err)
		case apiStatus(err) == http.StatusTooManyRequests || apiStatus(err) == http.StatusServiceUnavailable:
			if failures++; failures >= maxTries {
				return exportInfo{}, err
			}
		default:
			return exportInfo{}, err
		}
		d := derivedRetryDefault
		if err == nil && info.retry > 0 {
			d = info.retry
		} else if err != nil {
			d = derivedErrorWait(err)
		}
		if err := exportSleep(ctx, d); err != nil {
			return exportInfo{}, exportTimeout()
		}
	}
}

// readyExport reads an export's version once, and refuses one that is still being made.
func readyExport(ctx context.Context, cl *client.Client, nodeID, export string) (exportInfo, error) {
	info, err := exportProbe(ctx, cl, nodeID, export)
	if err != nil {
		if apiStatus(err) == http.StatusNotFound {
			return exportInfo{}, exportGone(export, err)
		}
		return exportInfo{}, exportRefusal(err)
	}
	if info.pending {
		return exportInfo{}, &exitcode.Error{Exit: exitcode.Failed, Name: "export_not_ready", Message: "the export " +
			"is not ready yet; run selfcloud export again"}
	}
	if !info.ranged {
		return exportInfo{}, errRangeIgnored
	}
	return info, nil
}

// exportReason is the API's details.reason of err, or "".
func exportReason(err error) string {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Reason
	}
	return ""
}

// exportRefusal maps a 415 whose reason is failed to a failure: the server tried to convert the file and could not,
// which is not a mistake in the arguments.
func exportRefusal(err error) error {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusUnsupportedMediaType && ae.Reason == "failed" {
		return &exitcode.Error{Exit: exitcode.Failed, Name: "failed", Message: ae.Message}
	}
	return err
}

// exportGone is the error of an export that answers as one that does not exist after the server accepted it.
func exportGone(export string, err error) error {
	msg := ""
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ": the server said " + strconv.Quote(ae.Message)
	}
	return &exitcode.Error{Exit: exitcode.Failed, Name: "export_gone", Message: "the export " + export + " is no longer " +
		"there; the file may have changed, so run selfcloud export again" + msg}
}

// exportTimeout is the error of a wait that ran out.
func exportTimeout() error {
	return &exitcode.Error{Exit: exitcode.Failed, Name: "timeout", Message: "the export is not ready yet; the server " +
		"goes on making it, so run selfcloud export again later"}
}

// exportReport writes where the export went and its size.
func exportReport(stdout io.Writer, g conn.Global, path string, size int64) int {
	if g.JSON {
		if err := output.JSON(stdout, map[string]any{"saved": path, "size": size}); err != nil {
			return fail(stdout, g, err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(stdout, []output.KV{{Key: "saved", Value: path},
		{Key: "size", Value: strconv.FormatInt(size, 10)}}); err != nil {
		return fail(stdout, g, err)
	}
	return exitcode.OK
}
