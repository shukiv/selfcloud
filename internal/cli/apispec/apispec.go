// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apispec is `selfcloud api spec`: the server's OpenAPI document, GET /api/v1/openapi.yaml. It goes to standard
// output, or to a file written through an os.Root with O_EXCL, so the file is never replaced unless --overwrite says so
// (design-cli.md 3.9).
package apispec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// Run is `selfcloud api spec [--out FILE [--overwrite]] [flags]`.
func Run(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("api spec")
	var g conn.Global
	g.Register(set)
	var out string
	var overwrite bool
	set.StringVar(&out, "out", "", "write the document to `file` instead of standard output")
	set.BoolVar(&overwrite, "overwrite", false, "replace the file named by --out when it exists")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("api spec takes no arguments"))
	}
	if g.JSON {
		return output.Fail(stderr, true, exitcode.Usagef("api spec writes YAML; --json does not apply"))
	}
	dir, name := filepath.Split(out)
	if dir == "" {
		dir = "."
	}
	if out != "" && (name == "" || name == "." || name == "..") {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("--out must name a file"))
	}
	c, err := conn.Connect(g, stderr, false)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := c.Client.Do(context.Background(), client.Request{
		Method: "GET",
		Path:   "/api/v1/openapi.yaml",
		Accept: "application/yaml",
	})
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	if !isYAML(resp.Header.Get("Content-Type")) {
		return output.Fail(stderr, g.JSON, errors.New("the server's answer is not the OpenAPI document (not YAML)"))
	}
	if out == "" {
		if _, err := stdout.Write(resp.Body); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		return exitcode.OK
	}
	if err := writeNew(dir, name, resp.Body, overwrite); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// isYAML reports whether a Content-Type names a YAML document.
func isYAML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mt {
	case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
		return true
	}
	return false
}

// writeNew writes data to name inside dir, through an os.Root, so no path can leave dir. Without overwrite the file is
// created with O_EXCL, which also refuses an existing symbolic link. With overwrite, a symbolic link or any other
// non-regular file at the name is refused first.
//
// With overwrite, the document is written to a new temporary name in the same root and renamed over the target, so a
// write that fails part-way leaves the original whole. The target is not truncated, and it is not removed before the
// new bytes are complete.
func writeNew(dir, name string, data []byte, overwrite bool) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if !overwrite {
		err := createNew(root, name, data, newFileMode)
		if errors.Is(err, fs.ErrExist) {
			return exitcode.Usagef("%s already exists; pass --overwrite to replace it", name)
		}
		return err
	}
	fi, err := root.Lstat(name)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return exitcode.Usagef("%s is not a regular file, so it is not replaced", name)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	// A replaced file keeps the mode it had; a new file is newFileMode.
	perm := os.FileMode(newFileMode)
	if err == nil {
		perm = fi.Mode().Perm()
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	tmp := ".api-spec-" + suffix + ".tmp"
	if err := createNew(root, tmp, data, perm); err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// newFileMode is the mode of a file api spec creates. The design names none; this is what it has always been.
const newFileMode = 0o644

// writeFault is a test seam: a non-nil error from it stands for a write that failed part-way.
var writeFault = func() error { return nil }

// randomSuffix returns a random name part for a temporary file.
func randomSuffix() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// createNew writes data to a new file name under root. The file is created with O_EXCL, so an existing name, a symbolic
// link included, is never written through. A failed write removes the new file.
func createNew(root *os.Root, name string, data []byte, perm os.FileMode) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		// Set after the create, so the umask does not change the mode.
		werr = f.Chmod(perm)
	}
	if werr == nil {
		werr = writeFault()
	}
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = root.Remove(name)
		return werr
	}
	return nil
}
