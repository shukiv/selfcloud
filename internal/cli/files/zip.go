// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// The zip commands (design-cli.md section 8): zip create asks the server to zip nodes and, with --wait,
// follows the zip until it is ready; zip get downloads a ready zip with the code get uses (part file, ranges, resume,
// checksum); zip rm removes one. A zip is made by a job on the server and belongs to the person who asked for it.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
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
	zipsRoute = "/api/v1/zips"
	// defaultWaitTimeout is how long --wait follows a zip before it gives up. The zip goes on being made on the server.
	defaultWaitTimeout = 30 * time.Minute
	// probeStall ends a probe whose answer has stopped moving for this long.
	probeStall = 30 * time.Second
)

// zipPollCap is the longest wait between two polls. Tests make it zero.
var zipPollCap = 5 * time.Second

// zipRoute is the route of one zip.
func zipRoute(zip string) string { return zipsRoute + "/" + zip }

// zipPollWait is the wait before the try-th poll of a zip that is still being made: half a second, doubling, at most five
// seconds. Tests make it zero.
var zipPollWait = func(try int) time.Duration {
	if try < 1 {
		try = 1
	}
	if try > 5 {
		return zipPollCap
	}
	return min(500*time.Millisecond<<(try-1), zipPollCap)
}

// zipIDs checks that every argument is a zip id. It is one check for all of them, before anything is sent.
func zipIDs(pos []string) ([]string, error) {
	if len(pos) == 0 {
		return nil, exitcode.Usagef("give one or more zip ids (zip_...)")
	}
	for _, arg := range pos {
		if !id.Valid("zip", arg) {
			return nil, exitcode.Usagef("a zip is named by its zip_ id")
		}
	}
	return pos, nil
}

// ZipCreate is `selfcloud zip create NODE... [--wait]`: zips one or more nodes, each a node id or a path in --folder. It
// prints the zip's id. With --wait it follows the zip until it is ready, and prints its size and how many items the
// server left out.
func ZipCreate(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("zip create")
	var g conn.Global
	g.Register(set)
	var folder string
	var wait bool
	var waitTimeout time.Duration
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder a path is walked from")
	set.BoolVar(&wait, "wait", false, "follow the zip until it is ready or has failed")
	set.DurationVar(&waitTimeout, "wait-timeout", defaultWaitTimeout, "with --wait, give up after this long; the zip "+
		"goes on being made")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	given := false
	set.Visit(func(f *flag.Flag) { given = given || f.Name == "wait-timeout" })
	switch {
	case given && !wait:
		return fail(stderr, g, exitcode.Usagef("--wait-timeout goes with --wait"))
	case waitTimeout <= 0:
		return fail(stderr, g, exitcode.Usagef("--wait-timeout must be longer than zero"))
	}
	refs, err := zipRoots(pos, folder)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	nodes := make([]string, 0, len(refs))
	for _, ref := range refs {
		nodeID := ref.id
		if ref.path != "" {
			if nodeID, err = findByPath(c, ref.folder, ref.path); err != nil {
				return fail(stderr, g, err)
			}
		}
		nodes = append(nodes, nodeID)
	}
	if firstRepeat(nodes) != "" {
		return fail(stderr, g, exitcode.Usagef("two of the arguments name the same node; a node is zipped once"))
	}
	r, err := jsonRequest(http.MethodPost, zipsRoute, map[string]any{"nodes": nodes})
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, r)
	if err != nil {
		return fail(stderr, g, err)
	}
	var job struct {
		Target string `json:"target"`
	}
	if json.Unmarshal(resp.Body, &job) != nil || !id.Valid("zip", job.Target) {
		return fail(stderr, g, errors.New("the server's answer names no zip"))
	}
	if !wait {
		if g.JSON {
			err = output.Indented(stdout, resp.Body)
		} else {
			err = output.KeyValues(stdout, []output.KV{{Key: "zip", Value: job.Target}})
		}
		if err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	info, err := waitForZip(ctx, c.Client, job.Target, waitTimeout)
	if err != nil {
		return fail(stderr, g, err)
	}
	if g.JSON {
		var doc map[string]any
		if err := json.Unmarshal(resp.Body, &doc); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not a document"))
		}
		doc["state"], doc["size"], doc["skipped"] = "ready", info.size, info.skipped
		err = output.JSON(stdout, doc)
	} else {
		err = output.KeyValues(stdout, []output.KV{
			{Key: "zip", Value: job.Target},
			{Key: "state", Value: "ready"},
			{Key: "size", Value: strconv.FormatInt(info.size, 10)},
			{Key: "skipped", Value: strconv.Itoa(info.skipped)},
		})
	}
	if err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// zipRef is one argument of zip create: a node id, or a path and the folder it is walked in.
type zipRef struct {
	id     string
	path   string
	folder string
}

// zipRoots checks the arguments of zip create before anything is sent.
func zipRoots(pos []string, folder string) ([]zipRef, error) {
	if len(pos) == 0 {
		return nil, exitcode.Usagef("zip create takes one or more nodes: node ids, or paths in --folder")
	}
	var refs []zipRef
	paths := false
	for _, arg := range pos {
		if !strings.HasPrefix(arg, "/") {
			nodeID, err := nodeArg(arg)
			if err != nil {
				return nil, err
			}
			refs = append(refs, zipRef{id: nodeID})
			continue
		}
		paths = true
		p, err := remotePath(arg)
		if err != nil {
			return nil, err
		}
		if p == "/" {
			return nil, exitcode.Usagef("zip create takes nodes, and / is the folder's root")
		}
		refs = append(refs, zipRef{path: p})
	}
	if !paths {
		if folder != "" {
			return nil, exitcode.Usagef("--folder goes with a path; a node id names its own folder")
		}
		return refs, nil
	}
	fid, err := requireFolder(folder)
	if err != nil {
		return nil, err
	}
	for i := range refs {
		refs[i].folder = fid
	}
	return refs, nil
}

// firstRepeat returns the first value that appears twice in list, or "".
func firstRepeat(list []string) string {
	seen := make(map[string]bool, len(list))
	for _, v := range list {
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return ""
}

// zipInfo is what the first byte of a zip says: the zip is ready, its size, its SHA-256, how many items the server left
// out, and the name it gives the file.
type zipInfo struct {
	pending bool
	// ranged is false when the server answered the range with the whole zip, as a proxy that drops Range does.
	ranged  bool
	etag    string
	sha     string
	size    int64
	skipped int
	name    string
}

// probeZip asks for the zip's first byte, so the answer carries the zip's state and, when it is ready, its version. 202 is
// a zip still being made; 206 is a ready one. A refusal comes back as the server's error.
func probeZip(ctx context.Context, cl *client.Client, zip string) (zipInfo, error) {
	h := http.Header{}
	h.Set("Range", "bytes=0-0")
	// The answer is read as a stream and its body is never read, so a proxy that drops the Range header, and answers with
	// the whole zip, costs no more than the headers.
	resp, err := cl.Stream(ctx, client.Request{Method: http.MethodGet, Path: zipRoute(zip), Header: h}, probeStall)
	if err != nil {
		return zipInfo{}, err
	}
	defer resp.Body.Close()
	var info zipInfo
	switch resp.Status {
	case http.StatusAccepted:
		return zipInfo{pending: true}, nil
	case http.StatusPartialContent:
		info.ranged = true
	case http.StatusOK:
	default:
		return zipInfo{}, fmt.Errorf("the server answered %d to a range of the zip", resp.Status)
	}
	info.etag = resp.Header.Get("ETag")
	if !validETag(info.etag) {
		return zipInfo{}, errors.New("the server gives the zip no valid version (etag)")
	}
	if b, err := hex.DecodeString(strings.Trim(info.etag, `"`)); err == nil && len(b) == 32 {
		info.sha = hex.EncodeToString(b)
	}
	// A ranged answer gives the size after the slash; a whole one, which the web also takes as ready, gives its length.
	sizeText := resp.Header.Get("Content-Length")
	if info.ranged {
		var ok bool
		if sizeText, ok = strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes 0-0/"); !ok {
			sizeText = ""
		}
	}
	if info.size, err = strconv.ParseInt(sizeText, 10, 64); err != nil || info.size < 1 {
		return zipInfo{}, errors.New("the server gives the zip no valid size")
	}
	if v := resp.Header.Get("X-Selfcloud-Skipped"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			info.skipped = n
		}
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		info.name = params["filename"]
	}
	return info, nil
}

// waitForZip polls the zip until it is ready. A zip that has failed answers as one that does not exist, so a 404 after
// the request was accepted is reported as a failed zip. The wait between polls grows to a cap; a poll that fails for a
// reason that may pass is made again, as a transfer's request is.
func waitForZip(ctx context.Context, cl *client.Client, zip string, limit time.Duration) (zipInfo, error) {
	pending, failures := 0, 0
	for {
		info, err := probeZip(ctx, cl, zip)
		switch {
		case ctx.Err() != nil:
			return zipInfo{}, zipTimeout(zip, limit)
		case err == nil && !info.pending:
			return info, nil
		case err == nil:
			pending++
			failures = 0
		case apiStatus(err) == http.StatusNotFound:
			return zipInfo{}, zipGone(zip, err)
		case !repeatable(err):
			return zipInfo{}, err
		default:
			if failures++; failures >= maxTries {
				return zipInfo{}, err
			}
			pending++
		}
		// The Retry-After of a pending zip is not read: the server gives the same five seconds for every zip, and a small
		// one is ready long before. A refusal's Retry-After is not read either, since the stream does not carry it.
		t := time.NewTimer(zipPollWait(pending))
		select {
		case <-ctx.Done():
			t.Stop()
			return zipInfo{}, zipTimeout(zip, limit)
		case <-t.C:
		}
	}
}

// zipGone is the error of a zip that answers as one that does not exist after the server accepted it. The server answers
// a failed zip, an expired one, a removed one and one whose items can no longer be read alike, so the message says so and
// keeps the server's own words.
func zipGone(zip string, err error) error {
	msg := ""
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ": the server said " + strconv.Quote(ae.Message)
	}
	return &exitcode.Error{Exit: exitcode.Failed, Name: "zip_failed", Message: "the zip " + zip + " failed, or it was " +
		"removed or expired, or one of its items can no longer be read; the server answers all of these as a zip that " +
		"does not exist" + msg}
}

// zipTimeout is the error of a wait that ran out. It names the zip, which the server goes on making.
func zipTimeout(zip string, limit time.Duration) error {
	return &exitcode.Error{Exit: exitcode.Failed, Name: "timeout", Message: fmt.Sprintf("the zip %s is not ready after "+
		"%s; the server goes on making it, so run selfcloud zip get %s later", zip, limit, zip)}
}

// apiStatus is the HTTP status of an API error, or 0.
func apiStatus(err error) int {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// ZipGet is `selfcloud zip get ZIP [DEST]`: downloads a ready zip as get downloads a file. DEST is a directory, where the
// zip takes the server's name, or a new file's path.
func ZipGet(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("zip get")
	var g conn.Global
	g.Register(set)
	var tf transferFlags
	tf.register(set)
	var overwrite bool
	set.BoolVar(&overwrite, "overwrite", false, "replace a regular file at the destination, once the download is "+
		"complete and checked; a part file this download cannot continue is replaced by a new one")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) < 1 || len(pos) > 2 {
		return fail(stderr, g, exitcode.Usagef("zip get takes a zip's id and a destination"))
	}
	zips, err := zipIDs(pos[:1])
	if err != nil {
		return fail(stderr, g, err)
	}
	zip := zips[0]
	piece, _, err := tf.pieceSize()
	if err != nil {
		return fail(stderr, g, err)
	}
	dest := ""
	if len(pos) == 2 {
		dest = pos[1]
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
	ctx := context.Background()
	info, err := readyZip(ctx, c.Client, zip)
	if err != nil {
		return fail(stderr, g, err)
	}
	name := info.name
	if name == "" {
		name = zip + ".zip"
	}
	subj := subject{key: zip, route: zipRoute(zip), name: name, verb: "zip get",
		ver: version{etag: info.etag, size: info.size, sha: info.sha},
		restat: func(ctx context.Context) (version, error) {
			again, err := readyZip(ctx, c.Client, zip)
			return version{etag: again.etag, size: again.size, sha: again.sha}, err
		}}
	got, err := downloadSubject(ctx, signedIn{c.Client}, subj, dst, dlOptions{piece: piece, stall: tf.stall,
		overwrite: overwrite}, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if info.skipped > 0 {
		noun := "items were"
		if info.skipped == 1 {
			noun = "item was"
		}
		fmt.Fprintf(stderr, "selfcloud: note: %d %s left out of the zip\n", info.skipped, noun)
	}
	return printGot(stdout, stderr, g, got)
}

// readyZip reads a zip's version, and refuses one that is still being made.
func readyZip(ctx context.Context, cl *client.Client, zip string) (zipInfo, error) {
	info, err := probeZip(ctx, cl, zip)
	if err != nil {
		return zipInfo{}, err
	}
	if info.pending {
		return zipInfo{}, &exitcode.Error{Exit: exitcode.Failed, Name: "zip_not_ready", Message: "the zip is not ready " +
			"yet; run selfcloud zip create --wait, or try again later"}
	}
	if !info.ranged {
		return zipInfo{}, errRangeIgnored
	}
	return info, nil
}

// ZipRm is `selfcloud zip rm ZIP...`: removes zips from the server now, rather than at their expiry. Asks first (--yes
// skips the question).
func ZipRm(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("zip rm")
	var g conn.Global
	g.Register(set)
	var yes bool
	set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	zips, err := zipIDs(pos)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := confirm(yes, phraseFor(zips), "remove the zip or zips from the server", stderr); err != nil {
		return fail(stderr, g, err)
	}
	return report(stdout, stderr, g.JSON, runEach(c, zips, func(zip string) request {
		return request{method: http.MethodDelete, path: zipRoute(zip)}
	}))
}
