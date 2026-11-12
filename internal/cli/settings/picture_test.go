// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

const (
	logoRoute = "/api/v1/settings/brand/logo"
	iconRoute = "/api/v1/settings/brand/icon"
)

const logoAnswer = `{"settings":[],"facts":[],"pictures":[
 {"key":"ui.brand.icon","state":"none","locked":false},
 {"key":"ui.brand.logo","state":"uploaded","type":"image/png","sha256":"abc123","locked":false}]}`

const noneAnswer = `{"settings":[],"facts":[],"pictures":[
 {"key":"ui.brand.icon","state":"none","locked":false},
 {"key":"ui.brand.logo","state":"none","locked":false}]}`

// writeFile makes a file called name in a new directory and returns its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func pngBytes() []byte {
	return append(append([]byte{}, pngSignature...), []byte("rest of the picture")...)
}

func TestLogoSetSendsThePictureAndPrintsItsLines(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodPut, logoRoute, reply{200, logoAnswer})
	data := pngBytes()
	r := f.run(LogoSet, writeFile(t, "logo.png", data))
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("PUT " + logoRoute)
	if ct := f.contentType(0); ct != "image/png" {
		t.Errorf("content type %q", ct)
	}
	if string(f.body(0)) != string(data) {
		t.Errorf("body %q, want the file's bytes", f.body(0))
	}
	want := "key=ui.brand.logo\nstate=uploaded\ntype=image/png\nsha256=abc123\nlocked=false\n"
	if r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
	j := f.run(LogoSet, writeFile(t, "logo.png", data), "--json")
	sameJSON(t, j.stdout, logoAnswer)
}

func TestIconSetGoesToTheIconRoute(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodPut, iconRoute, reply{200, `{"settings":[],"facts":[],"pictures":[
	 {"key":"ui.brand.icon","state":"uploaded","type":"image/png","sha256":"def","locked":false}]}`})
	r := f.run(IconSet, writeFile(t, "icon.png", pngBytes()))
	if r.code != 0 || !strings.HasPrefix(r.stdout, "key=ui.brand.icon\nstate=uploaded\n") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	f.wantCalls("PUT " + iconRoute)
}

func TestASvgIsSentAsSvgWhateverTheCase(t *testing.T) {
	for _, name := range []string{"logo.svg", "LOGO.SVG"} {
		isolate(t, adminKey)
		f := newFake(t)
		f.on(http.MethodPut, logoRoute, reply{200, logoAnswer})
		r := f.run(LogoSet, writeFile(t, name, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")))
		if r.code != 0 || f.contentType(0) != "image/svg+xml" {
			t.Errorf("%s: exit %d, type %q, stderr %q", name, r.code, f.contentType(0), r.stderr)
		}
	}
}

func TestAFileThatIsNotAPictureIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, name := range []string{"notes.txt", "x.png", "x.jpg"} {
		isolate(t, adminKey)
		f := newFake(t)
		r := f.run(LogoSet, writeFile(t, name, []byte("plain text")))
		if r.code != 2 || !strings.Contains(r.stderr, "is not a PNG or an SVG") {
			t.Errorf("%s: exit %d, stderr %q", name, r.code, r.stderr)
		}
		f.wantCalls()
	}
}

func TestAMissingFileOrADirectoryOrTheWrongCountIsRefusedBeforeAnyRequest(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	dir := t.TempDir()
	if r := f.run(LogoSet, filepath.Join(dir, "absent.png")); r.code != 4 {
		t.Errorf("missing: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := f.run(LogoSet, dir); r.code != 2 {
		t.Errorf("directory: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := f.run(LogoSet); r.code != 2 {
		t.Errorf("no file: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := f.run(LogoSet, "a.png", "b.png"); r.code != 2 {
		t.Errorf("two files: exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls()
}

func TestAFileOverFourMiBIsRefusedWithoutReadingIt(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	p := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(p, pngSignature, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, 4<<20+1); err != nil {
		t.Fatal(err)
	}
	r := f.run(LogoSet, p)
	if r.code != 2 || !strings.Contains(r.stderr, "is too large to be a logo or an icon (over 4 MiB)") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls()
}

func TestThePictureRoutesRefusalsKeepTheServersMessage(t *testing.T) {
	cases := []struct {
		name   string
		reply  reply
		code   int
		stderr string
	}{
		{"too large", reply{413, `{"error":{"code":"too_large","message":"the picture is over 256 KiB"}}`}, 2, "the picture is over 256 KiB"},
		{"unsupported", reply{415, `{"error":{"code":"unsupported_media_type","message":"send a PNG or an SVG"}}`}, 2, "send a PNG or an SVG"},
		{"invalid", reply{400, `{"error":{"code":"invalid_picture","message":"the icon must be square"}}`}, 2, "the icon must be square"},
		{"locked", reply{409, `{"error":{"code":"conflict","message":"the logo is set by the config file",
		 "details":{"reason":"setting_locked","keys":["ui.brand.logo"]}}}`}, 3, "keys: ui.brand.logo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, adminKey)
			f := newFake(t)
			f.on(http.MethodPut, logoRoute, tc.reply)
			r := f.run(LogoSet, writeFile(t, "logo.png", pngBytes()))
			if r.code != tc.code || !strings.Contains(r.stderr, tc.stderr) {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
		})
	}
}

func TestLogoRemoveSendsTheRemoveRequestAndPrintsNone(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodDelete, logoRoute, reply{200, noneAnswer})
	r := f.run(LogoRemove)
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("DELETE " + logoRoute)
	if !strings.Contains(r.stdout, "key=ui.brand.logo\nstate=none\n") {
		t.Errorf("stdout %q", r.stdout)
	}
	j := f.run(LogoRemove, "--json")
	sameJSON(t, j.stdout, noneAnswer)
}

func TestIconRemoveGoesToTheIconRouteAndTakesNoArgument(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodDelete, iconRoute, reply{200, noneAnswer})
	if r := f.run(IconRemove, "extra"); r.code != 2 {
		t.Errorf("an argument: exit %d", r.code)
	}
	f.wantCalls()
	if r := f.run(IconRemove); r.code != 0 || !strings.HasPrefix(r.stdout, "key=ui.brand.icon\nstate=none\n") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

func TestPictureRoutesHintAtTheAdminKeyOn403(t *testing.T) {
	isolate(t, accountKey)
	f := newFake(t)
	f.on(http.MethodPut, logoRoute, reply{403, `{"error":{"code":"forbidden","message":"an installation admin is required"}}`})
	r := f.run(LogoSet, writeFile(t, "logo.png", pngBytes()))
	if r.code != 3 || !strings.Contains(r.stderr, hint) {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}
