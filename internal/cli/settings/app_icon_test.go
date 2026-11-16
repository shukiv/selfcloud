// SPDX-License-Identifier: AGPL-3.0-or-later

package settings

import (
	"net/http"
	"strings"
	"testing"
)

const appIconRoute = "/api/v1/settings/brand/app-icon"

const appIconAnswer = `{"settings":[],"facts":[],"pictures":[
 {"key":"ui.brand.app_icon","state":"uploaded","type":"image/png","sha256":"abc123","locked":false},
 {"key":"ui.brand.icon","state":"none","locked":false},
 {"key":"ui.brand.logo","state":"none","locked":false}]}`

const appIconNoneAnswer = `{"settings":[],"facts":[],"pictures":[
 {"key":"ui.brand.app_icon","state":"none","locked":false},
 {"key":"ui.brand.icon","state":"none","locked":false},
 {"key":"ui.brand.logo","state":"none","locked":false}]}`

func TestAppIconSetGoesToTheAppIconRoute(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodPut, appIconRoute, reply{200, appIconAnswer})
	data := pngBytes()
	r := f.run(AppIconSet, writeFile(t, "app.png", data))
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls("PUT " + appIconRoute)
	if ct := f.contentType(0); ct != "image/png" {
		t.Errorf("content type %q", ct)
	}
	if string(f.body(0)) != string(data) {
		t.Errorf("body %q, want the file's bytes", f.body(0))
	}
	want := "key=ui.brand.app_icon\nstate=uploaded\ntype=image/png\nsha256=abc123\nlocked=false\n"
	if r.stdout != want {
		t.Errorf("stdout %q, want %q", r.stdout, want)
	}
	j := f.run(AppIconSet, writeFile(t, "app.png", data), "--json")
	sameJSON(t, j.stdout, appIconAnswer)
}

func TestAppIconRemoveGoesToTheAppIconRouteAndTakesNoArgument(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	f.on(http.MethodDelete, appIconRoute, reply{200, appIconNoneAnswer})
	if r := f.run(AppIconRemove, "extra"); r.code != 2 {
		t.Errorf("an argument: exit %d", r.code)
	}
	f.wantCalls()
	r := f.run(AppIconRemove)
	if r.code != 0 || !strings.HasPrefix(r.stdout, "key=ui.brand.app_icon\nstate=none\n") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	f.wantCalls("DELETE " + appIconRoute)
}

func TestAppIconSetRefusesANonPictureBeforeAnyRequest(t *testing.T) {
	isolate(t, adminKey)
	f := newFake(t)
	r := f.run(AppIconSet, writeFile(t, "notes.txt", []byte("plain text")))
	if r.code != 2 || !strings.Contains(r.stderr, "is not a PNG or an SVG") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
	f.wantCalls()
}
