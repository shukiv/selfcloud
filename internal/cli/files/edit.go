// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// editLimit is the size of a text file the code editor opens, as web/src/lib/code/edit.ts has it: 2 MiB.
const editLimit = 2 << 20

// editSaved is what edit prints after the server saves the text.
type editSaved struct {
	Size int64  `json:"size"`
	ETag string `json:"etag"`
}

// Edit is `selfcloud edit NODE | --folder FOLDER PATH`: open a small text file in the person's editor, then save it
// with If-Match if it changed (decision 173). A file that changed on the server after it was opened is not overwritten:
// the edited text is kept in a temporary file and its path is printed (exit 8).
func Edit(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("edit")
	var g conn.Global
	g.Register(set)
	var folder string
	set.StringVar(&folder, "folder", "", "the `id` (fld_...) of the folder a path is walked from")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("edit takes one file: a node id, or a path in --folder"))
	}
	var nodeID, remote string
	var err error
	if strings.HasPrefix(pos[0], "/") {
		if folder, err = requireFolder(folder); err != nil {
			return fail(stderr, g, err)
		}
		if remote, err = remotePath(pos[0]); err != nil {
			return fail(stderr, g, err)
		}
		if remote == "/" {
			return fail(stderr, g, exitcode.Usagef("edit takes a file, and / is the folder's root"))
		}
	} else {
		if folder != "" {
			return fail(stderr, g, exitcode.Usagef("--folder goes with a path; a node id names its own folder"))
		}
		if nodeID, err = nodeArg(pos[0]); err != nil {
			return fail(stderr, g, err)
		}
	}
	if !IsTerminal(Stdin) || !isTerminalOutput(stdout) {
		return fail(stderr, g, exitcode.Usagef("edit opens an editor, and needs a terminal"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	ctx := context.Background()
	if remote != "" {
		if nodeID, err = findByPath(c, folder, remote); err != nil {
			return fail(stderr, g, err)
		}
	}
	v, err := statOf(ctx, c.Client, nodeID)
	if err != nil {
		return fail(stderr, g, err)
	}
	switch {
	case v.Kind == "dir":
		return fail(stderr, g, exitcode.Usagef("the node is a directory; edit takes a file"))
	case v.Kind != "file":
		return fail(stderr, g, exitcode.Usagef("edit takes a file, and the node is not one"))
	case v.Size > editLimit:
		return fail(stderr, g, exitcode.Usagef("the file is %d bytes, larger than the %d-byte (2 MiB) limit of edit",
			v.Size, editLimit))
	}
	resp, err := c.Client.Do(ctx, client.Request{Method: http.MethodGet, Path: nodeRoute(nodeID, "/content")})
	if err != nil {
		return fail(stderr, g, err)
	}
	orig := resp.Body
	if len(orig) > editLimit {
		return fail(stderr, g, exitcode.Usagef("the file is larger than the %d-byte (2 MiB) limit of edit", editLimit))
	}
	dir, err := os.MkdirTemp("", "selfcloud-edit-*")
	if err != nil {
		return fail(stderr, g, fmt.Errorf("a temporary file for the editor cannot be made: %w", err))
	}
	path := filepath.Join(dir, editName(v.Name))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return fail(stderr, g, fmt.Errorf("a temporary file for the editor cannot be made: %w", err))
	}
	if _, err := f.Write(orig); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return fail(stderr, g, fmt.Errorf("the temporary file for the editor cannot be written: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.RemoveAll(dir)
		return fail(stderr, g, fmt.Errorf("the temporary file for the editor cannot be written: %w", err))
	}
	if err := runEditor(path); err != nil {
		_ = os.RemoveAll(dir)
		return fail(stderr, g, err)
	}
	newText, err := os.ReadFile(path)
	if err != nil {
		return fail(stderr, g, fmt.Errorf("the editor's file cannot be read; your text is kept in %s: %w", path, err))
	}
	if bytes.Equal(newText, orig) {
		_ = os.RemoveAll(dir)
		if g.JSON {
			if err := output.JSON(stdout, map[string]bool{"unchanged": true}); err != nil {
				return fail(stderr, g, err)
			}
		} else if _, err := io.WriteString(stdout, "no change; nothing was saved\n"); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	switch {
	case !utf8.Valid(newText):
		return fail(stderr, g, exitcode.Usagef("the edited text is not UTF-8; nothing was sent, and your text is kept in %s",
			path))
	case bytes.IndexByte(newText, 0) >= 0:
		return fail(stderr, g, exitcode.Usagef("the edited text holds a NUL byte; nothing was sent, and your text is kept in %s",
			path))
	case len(newText) > editLimit:
		return fail(stderr, g, exitcode.Usagef("the edited text is %d bytes, larger than the %d-byte (2 MiB) limit; "+
			"nothing was sent, and your text is kept in %s", len(newText), editLimit, path))
	}
	saved, err := c.Client.Do(ctx, client.Request{
		Method:      http.MethodPut,
		Path:        nodeRoute(nodeID, "/content"),
		ContentType: v.MimeType,
		Body:        newText,
		Header:      http.Header{"If-Match": {v.ETag}},
	})
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusPreconditionFailed {
			return fail(stderr, g, exitcode.Conflictf("the file changed on the server after it was opened; your text is "+
				"kept in %s", path))
		}
		return fail(stderr, g, fmt.Errorf("%w; your text is kept in %s", err, path))
	}
	node, err := decodeNode(saved.Body)
	if err != nil {
		_ = os.RemoveAll(dir)
		return fail(stderr, g, errors.New("the file was saved, but the server's answer is not a node"))
	}
	_ = os.RemoveAll(dir)
	if g.JSON {
		if err := output.JSON(stdout, editSaved{Size: node.Size, ETag: node.ETag}); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(stdout, []output.KV{
		{Key: "size", Value: strconv.FormatInt(node.Size, 10)},
		{Key: "etag", Value: node.ETag},
	}); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// isTerminalOutput reports whether a command's standard output is a terminal, where an editor may draw.
func isTerminalOutput(w io.Writer) bool {
	r, ok := w.(io.Reader)
	return ok && IsTerminal(r)
}

// runEditor opens path in the person's editor: VISUAL, then EDITOR, then vi. The editor gets the terminal, so a full
// screen editor works; the shell quotes the path, so spaces, quotes and dollars in the name reach the editor as they are.
func runEditor(path string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	cmd := exec.Command("sh", "-c", ed+` "$1"`, "selfcloud-edit", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGQUIT)
	defer signal.Stop(ch)
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("the editor exited with status %d; nothing was saved", ee.ExitCode())
	}
	return fmt.Errorf("the editor could not be started; nothing was saved: %w", err)
}

// maxEditName bounds the temporary file's name, since a name the server accepts may be far longer than a file system
// takes.
const maxEditName = 100

// editName turns a node's name into a file name inside the temporary directory: separators and control characters are
// dropped, so the file cannot leave the directory, a leading dot, dash or space is dropped, so it is not hidden or an
// option, and a long name keeps its extension but no more than maxEditName bytes.
func editName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimLeft(b.String(), ".- ")
	if s == "" {
		return "file"
	}
	ext := path.Ext(s)
	stem := strings.TrimSuffix(s, ext)
	if len(ext) > 50 {
		stem, ext = s, ""
	}
	stem = truncateBytes(stem, maxEditName-len(ext))
	if stem == "" {
		stem = "file"
	}
	return stem + ext
}

// truncateBytes cuts s to at most n bytes, without leaving half a rune.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
