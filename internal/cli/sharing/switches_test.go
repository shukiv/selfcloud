// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// switchCase names a switch command, the field it sends and how to run it.
type switchCase struct {
	name  string
	field string
	fn    func([]string, io.Writer, io.Writer) int
}

func switches() []switchCase {
	return []switchCase{
		{name: "share no-download", field: "no_download", fn: ShareNoDownload},
		{name: "share editors-can-share", field: "editors_can_share", fn: ShareEditorsCanShare},
	}
}

func TestShareSwitchesSendOneField(t *testing.T) {
	for _, sw := range switches() {
		for _, c := range []struct {
			arg  string
			want string
		}{{"on", "true"}, {"off", "false"}} {
			isolate(t, testKey)
			f := newFake(t)
			f.on("PATCH", nodeRoute(testNode, ""), ok(map[string]any{"id": testNode, sw.field: c.want == "true"}))
			res := f.run(sw.fn, testNode, c.arg)
			wantCode(t, res, 0)
			f.wantCalls("PATCH " + nodeRoute(testNode, ""))
			wantBody := `{"` + sw.field + `":` + c.want + `}`
			if got := f.rawBody(0); got != wantBody {
				t.Fatalf("%s %s: body %q, want %q", sw.name, c.arg, got, wantBody)
			}
			if !strings.Contains(res.stdout, sw.field+"="+c.want+"\n") {
				t.Fatalf("%s %s: stdout %q, want %s=%s", sw.name, c.arg, res.stdout, sw.field, c.want)
			}
		}
	}
}

func TestShareSwitchesRefuseABadSwitch(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, sw := range switches() {
		for _, args := range [][]string{
			{testNode},
			{},
			{"on"},
			{testNode, "true"},
			{testNode, "1"},
			{testNode, "yes"},
			{testNode, "ON"},
			{testNode, ""},
			{testNode, "on", "off"},
			{testNode, "off", "extra"},
		} {
			wantCode(t, f.run(sw.fn, args...), 2)
		}
	}
	f.wantNoCalls()
}

func TestShareSwitchesRefuseANodeThatIsNotANode(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	for _, sw := range switches() {
		for _, node := range []string{"not-a-node", "/docs", testFolder, testUser, "nod_short"} {
			wantCode(t, f.run(sw.fn, node, "on"), 2)
		}
	}
	f.wantNoCalls()
}

func TestShareSwitchesReportTheServersRefusals(t *testing.T) {
	for _, sw := range switches() {
		isolate(t, testKey)
		f := newFake(t)
		f.on("PATCH", nodeRoute(testNode, ""),
			apiErr(http.StatusForbidden, "forbidden", "you may not change this node's sharing"),
			apiErr(http.StatusNotFound, "not_found", "no such node"))
		res := f.run(sw.fn, testNode, "on")
		wantCode(t, res, 3)
		if !strings.Contains(res.stderr, "you may not change this node's sharing") {
			t.Fatalf("%s: stderr %q does not say why", sw.name, res.stderr)
		}
		wantCode(t, f.run(sw.fn, testNode, "off"), 4)
	}
}

func TestShareSwitchesJSONIsTheServersDocument(t *testing.T) {
	isolate(t, testKey)
	f := newFake(t)
	f.on("PATCH", nodeRoute(testNode, ""),
		ok(map[string]any{"id": testNode, "no_download": true, "editors_can_share": false}))
	res := f.run(ShareNoDownload, testNode, "on", "--json")
	wantCode(t, res, 0)
	if !strings.HasPrefix(res.stdout, "{") || !strings.Contains(res.stdout, `"no_download": true`) ||
		!strings.Contains(res.stdout, `"editors_can_share": false`) {
		t.Fatalf("stdout %q is not the server's document", res.stdout)
	}
}
