// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// maxPictureFile is the largest file the command reads. The server's own cap is smaller (256 KiB) and its 413 answers the
// files between the two; this limit only stops the command from reading a file that is plainly not a picture.
const maxPictureFile = 4 << 20

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// pictureKey is the settings key of the logo or the icon, as the answer's pictures name it.
func pictureKey(which string) string { return "ui.brand." + which }

// pictureRoute is the route of the logo or the icon.
func pictureRoute(which string) string { return settingsPath + "/brand/" + which }

// LogoSet is `selfcloud settings logo set FILE [flags]`.
func LogoSet(args []string, stdout, stderr io.Writer) int { return set("logo", args, stdout, stderr) }

// IconSet is `selfcloud settings icon set FILE [flags]`.
func IconSet(args []string, stdout, stderr io.Writer) int { return set("icon", args, stdout, stderr) }

// LogoRemove is `selfcloud settings logo remove [flags]`.
func LogoRemove(args []string, stdout, stderr io.Writer) int {
	return remove("logo", args, stdout, stderr)
}

// IconRemove is `selfcloud settings icon remove [flags]`.
func IconRemove(args []string, stdout, stderr io.Writer) int {
	return remove("icon", args, stdout, stderr)
}

// set sends a file as the logo or the icon. The file is judged whole before any request.
func set(which string, args []string, stdout, stderr io.Writer) int {
	g, pos, code, ok := parseFlags("settings "+which+" set", args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return output.Fail(stderr, g.JSON, usage("settings %s set takes one FILE", which))
	}
	data, mediaType, err := readPicture(pos[0])
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return pictureCall(c, g, stdout, stderr, which, client.Request{
		Method: http.MethodPut, Path: pictureRoute(which), Accept: "application/json", ContentType: mediaType, Body: data,
	})
}

// remove deletes the uploaded logo or icon.
func remove(which string, args []string, stdout, stderr io.Writer) int {
	g, pos, code, ok := parseFlags("settings "+which+" remove", args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, usage("settings %s remove takes no argument", which))
	}
	c, err := conn.Connect(g, stderr, true)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return pictureCall(c, g, stdout, stderr, which, client.Request{
		Method: http.MethodDelete, Path: pictureRoute(which), Accept: "application/json",
	})
}

// pictureCall sends the request and prints the picture it answers with, or the document under --json.
func pictureCall(c *conn.Conn, g conn.Global, stdout, stderr io.Writer, which string, req client.Request) int {
	resp, err := c.Client.Do(context.Background(), req)
	if err != nil {
		return fail(stderr, g.JSON, c.Kind, err)
	}
	if g.JSON {
		return printJSON(stdout, stderr, resp.Body)
	}
	a, err := parseAnswer(resp.Body)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	p, _, found := a.picture(pictureKey(which))
	if !found {
		return output.Fail(stderr, g.JSON, errors.New("the server's answer has no "+which))
	}
	if err := pictureLines(stdout, p); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

// readPicture reads the file and decides its type from its content: the PNG signature makes a PNG; otherwise only a name
// ending in .svg makes an SVG. The extension alone never makes a PNG, so a text file named x.png is refused here.
func readPicture(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, "", exitcode.NotFoundf("%s: no such file", path)
	case err != nil:
		return nil, "", err
	case !info.Mode().IsRegular():
		return nil, "", usage("%s is not a regular file", path)
	case info.Size() > maxPictureFile:
		return nil, "", usage("%s is too large to be a logo or an icon (over 4 MiB)", path)
	}
	// The size was judged from Stat, but the file may grow before it is read: read at most one byte past the limit.
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPictureFile+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxPictureFile {
		return nil, "", usage("%s is too large to be a logo or an icon (over 4 MiB)", path)
	}
	switch {
	case bytes.HasPrefix(data, pngSignature):
		return data, "image/png", nil
	case strings.HasSuffix(strings.ToLower(path), ".svg"):
		return data, "image/svg+xml", nil
	}
	return nil, "", usage("%s is not a PNG or an SVG", path)
}
