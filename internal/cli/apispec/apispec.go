// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apispec is `selfcloud api spec`: the server's OpenAPI document, GET /api/v1/openapi.yaml. It goes to standard
// output, or to a file written through an os.Root with O_EXCL, so the file is never replaced unless --overwrite says so
// (design-cli.md 3.9).
package apispec

import (
	"context"
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
func writeNew(dir, name string, data []byte, overwrite bool) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		fi, err := root.Lstat(name)
		switch {
		case err == nil && !fi.Mode().IsRegular():
			return exitcode.Usagef("%s is not a regular file, so it is not replaced", name)
		case err == nil:
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}
	f, err := root.OpenFile(name, flags, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exitcode.Usagef("%s already exists; pass --overwrite to replace it", name)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = root.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(name)
		return err
	}
	return nil
}
