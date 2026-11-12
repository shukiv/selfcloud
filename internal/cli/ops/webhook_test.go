// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// hookDoc returns an endpoint document as the API answers a read, with fields added or replaced by extra.
func hookDoc(extra map[string]any) map[string]any {
	d := map[string]any{
		"id": testHook, "url": "https://hooks.example.test/in", "description": "orders", "events": []string{"file.created", "file.deleted"},
		"state": "active", "disabled_reason": nil, "consecutive_failures": 0, "last_success_at": "2026-11-02T10:00:00Z",
		"last_failure_at": nil, "previous_secret_until": nil, "created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-02T10:00:00Z",
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// deliveryDoc returns a delivery document as the API answers it.
func deliveryDoc(extra map[string]any) map[string]any {
	d := map[string]any{
		"id": testDelivery, "event_id": "evt_1", "type": "file.created", "state": "failed", "attempts": 3, "last_code": 500,
		"last_error": "http_status", "next_attempt_at": "2026-11-02T10:05:00Z", "created_at": "2026-11-02T10:00:00Z",
		"finished_at": "2026-11-02T10:06:00Z",
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// webhookCommand is a command that names an account, with the arguments that make it valid apart from the account.
type webhookCommand struct {
	name string
	run  func([]string, io.Writer, io.Writer) int
	args []string
}

func webhookCommands() []webhookCommand {
	return []webhookCommand{
		{"webhook add", WebhookAdd, []string{"--url", "https://hooks.example.test/in", "--event", "file.created"}},
		{"webhook ls", WebhookLs, nil},
		{"webhook show", WebhookShow, []string{testHook}},
		{"webhook update", WebhookUpdate, []string{testHook, "--state", "paused"}},
		{"webhook rm", WebhookRm, []string{testHook, "--yes"}},
		{"webhook rotate-secret", WebhookRotateSecret, []string{testHook, "--yes"}},
		{"webhook test", WebhookTest, []string{testHook}},
		{"webhook deliveries", WebhookDeliveries, []string{testHook}},
		{"webhook retry", WebhookRetry, []string{testHook, testDelivery}},
	}
}

// TestEveryWebhookCommandChecksItsAccountBeforeAnyRequest: with no account, a flag that is not an account id, or a
// variable that is not one, each command is refused with exit 2 and sends nothing.
func TestEveryWebhookCommandChecksItsAccountBeforeAnyRequest(t *testing.T) {
	for _, c := range webhookCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			wantCode(t, f.run(c.run, c.args...), 2)
			for _, bad := range []string{"acc_short", testFolder, testAcc + "/x", testAcc + "?x=1"} {
				wantCode(t, f.run(c.run, append(append([]string{}, c.args...), "--account", bad)...), 2)
			}
			t.Setenv("SELFCLOUD_ACCOUNT", "acc_short")
			wantCode(t, f.run(c.run, c.args...), 2)
			f.wantNoCalls()
		})
	}
}

// TestTheAccountComesFromTheEnvironmentWhenNoFlagIsGiven: SELFCLOUD_ACCOUNT names the account, and --account wins.
func TestTheAccountComesFromTheEnvironmentWhenNoFlagIsGiven(t *testing.T) {
	isolate(t, testAccountKey)
	t.Setenv("SELFCLOUD_ACCOUNT", testAcc)
	f := newFake(t)
	f.on("GET", accHooks(), page(""))
	wantCode(t, f.run(WebhookLs), 0)
	f.wantCalls("GET " + accHooks())
}

const accountHintText = "this needs an account key; a signed-in device cannot manage an account"

// TestADeviceTokenIsToldAnAccountNeedsAKey: the server answers a device token not found on every route of an account, and
// the person is told why. The refusal is exit 4, and it is not told under --json.
func TestADeviceTokenIsToldAnAccountNeedsAKey(t *testing.T) {
	for _, c := range webhookCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			f := newFake(t) // every route answers 404 no such thing
			res := f.runAcc(c.run, c.args...)
			wantCode(t, res, 4)
			if !strings.Contains(res.stderr, accountHintText) {
				t.Fatalf("stderr %q lacks the hint", res.stderr)
			}
			res = f.runAcc(c.run, append(append([]string{}, c.args...), "--json")...)
			wantCode(t, res, 4)
			if strings.Contains(res.stderr, "hint") || !strings.HasPrefix(res.stderr, `{"error"`) {
				t.Fatalf("--json stderr %q: want only the JSON error", res.stderr)
			}
		})
	}
}

// TestTheAccountHintIsForADeviceTokenOnly: a key is not told it, and neither is another failure of a device token.
func TestTheAccountHintIsForADeviceTokenOnly(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(WebhookShow, testHook)
	wantCode(t, res, 4)
	if strings.Contains(res.stderr, "hint") {
		t.Fatalf("a key got a hint: %q", res.stderr)
	}
	isolate(t, testDeviceTok)
	f = newFake(t)
	f.set("POST", accHooks(), apiErrWith(403, "forbidden", "a terminal's sign-in does not make this; use the browser", "device_token"))
	res = f.runAcc(WebhookAdd, "--url", "https://hooks.example.test/in", "--event", "file.created")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint") || !strings.Contains(res.stderr, "device_token") && !strings.Contains(res.stderr, "forbidden") {
		t.Fatalf("a device_token refusal: stderr %q", res.stderr)
	}
}

// TestWebhookAddSendsTheEndpointAndShowsTheSecretOnce: the request carries the url, the events sorted as given and the
// description; the answer's secret is printed once, with the endpoint, and a note says it is not shown again.
func TestWebhookAddSendsTheEndpointAndShowsTheSecretOnce(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), doc(201, hookDoc(map[string]any{"secret": testSecret})))
	res := f.runAcc(WebhookAdd, "--url", "https://hooks.example.test/in", "--event", "file.created", "--event", "file.deleted,upload.completed",
		"--description", "orders")
	wantCode(t, res, 0)
	f.wantCalls("POST " + accHooks())
	f.wantBearer(testAccountKey)
	body := f.bodyOf(0)
	if body["url"] != "https://hooks.example.test/in" || body["description"] != "orders" {
		t.Fatalf("body %v", body)
	}
	evs, _ := json.Marshal(body["events"])
	if string(evs) != `["file.created","file.deleted","upload.completed"]` {
		t.Fatalf("events %s", evs)
	}
	if strings.Count(res.stdout, testSecret) != 1 || !strings.Contains(res.stdout, "secret="+testSecret+"\n") {
		t.Fatalf("stdout %q: want the secret once", res.stdout)
	}
	for _, want := range []string{"id=" + testHook + "\n", "url=https://hooks.example.test/in\n", "events=file.created,file.deleted\n", "state=active\n"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.stdout)
		}
	}
	if strings.Contains(res.stderr, testSecret) || !strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("stderr %q: want a note and no secret", res.stderr)
	}
}

// TestWebhookAddJSONHoldsTheSecretOnce: --json prints the document with the secret in it, and no note.
func TestWebhookAddJSONHoldsTheSecretOnce(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), doc(201, hookDoc(map[string]any{"secret": testSecret})))
	res := f.runAcc(WebhookAdd, "--url", "https://hooks.example.test/in", "--event", "file.created", "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got["secret"] != testSecret || got["id"] != testHook {
		t.Fatalf("document %v", got)
	}
	if strings.Contains(res.stderr, testSecret) || strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

// TestWebhookAddRefusesBadInputBeforeAnyRequest: the form of the url and the events are checked locally, and a refusal
// never repeats the url, which may hold a password or a token.
func TestWebhookAddRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no url", []string{"--event", "file.created"}},
		{"no event", []string{"--url", "https://hooks.example.test/in"}},
		{"empty event", []string{"--url", "https://hooks.example.test/in", "--event", "file.created,"}},
		{"unknown event", []string{"--url", "https://hooks.example.test/in", "--event", "file.exploded"}},
		{"relative url", []string{"--url", "/hook", "--event", "file.created"}},
		{"ftp", []string{"--url", "ftp://hooks.example.test/in", "--event", "file.created"}},
		{"no host", []string{"--url", "https:///in", "--event", "file.created"}},
		{"userinfo", []string{"--url", "https://alice:hunter2pass@hooks.example.test/in", "--event", "file.created"}},
		{"user only", []string{"--url", "https://tokenvalue123@hooks.example.test/in", "--event", "file.created"}},
		{"garbage", []string{"--url", "https://hooks.example.test/%zz?key=sekrit", "--event", "file.created"}},
		{"space", []string{"--url", "https://hooks.example.test/a b?key=sekrit", "--event", "file.created"}},
		{"too long", []string{"--url", "https://hooks.example.test/" + strings.Repeat("a", 2100), "--event", "file.created"}},
		{"long description", []string{"--url", "https://hooks.example.test/in", "--event", "file.created", "--description", strings.Repeat("d", 201)}},
		{"extra argument", []string{"--url", "https://hooks.example.test/in", "--event", "file.created", "extra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			res := f.runAcc(WebhookAdd, c.args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
			for _, leak := range []string{"hunter2pass", "tokenvalue123", "sekrit", "alice"} {
				if strings.Contains(res.stdout+res.stderr, leak) {
					t.Fatalf("the refusal repeats %q: %q", leak, res.stderr)
				}
			}
		})
	}
}

// TestWebhookAddAcceptsPlainHTTPForTheServerToJudge: the form is checked here and the network policy is the server's, so
// an http url, a loopback host and a private address go to the server, and its refusal is shown as it comes.
func TestWebhookAddAcceptsPlainHTTPForTheServerToJudge(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), apiErr(400, "invalid_request", "url cannot be used: its host is not allowed"))
	res := f.runAcc(WebhookAdd, "--url", "http://127.0.0.1:9000/in", "--event", "file.created")
	wantCode(t, res, 2)
	f.wantCalls("POST " + accHooks())
	if !strings.Contains(res.stderr, "url cannot be used: its host is not allowed") {
		t.Fatalf("stderr %q: want the server's refusal", res.stderr)
	}
}

// TestWebhookAddWithoutDescriptionSendsNone: no --description sends no description key.
func TestWebhookAddWithoutDescriptionSendsNone(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), doc(201, hookDoc(map[string]any{"secret": testSecret})))
	wantCode(t, f.runAcc(WebhookAdd, "--url", "https://hooks.example.test/in", "--event", "file.created"), 0)
	if _, has := f.bodyOf(0)["description"]; has {
		t.Fatalf("body %s has a description", f.rawBody(0))
	}
}

// TestWebhookLsListsEndpointsAndFollowsPages: a table of id, state, events and url; --all follows the cursor; without it
// a note says more exist.
func TestWebhookLsListsEndpointsAndFollowsPages(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accHooks(), page("c2", hookDoc(nil)), page("", hookDoc(map[string]any{"id": "whk_second"})))
	res := f.runAcc(WebhookLs)
	wantCode(t, res, 0)
	want := table([]string{"ID", "STATE", "EVENTS", "URL"},
		[]string{testHook, "active", "file.created,file.deleted", "https://hooks.example.test/in"})
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant\n%q", res.stdout, want)
	}
	if !strings.Contains(res.stderr, "more items exist") {
		t.Fatalf("stderr %q: want the note", res.stderr)
	}
	f = newFake(t)
	f.on("GET", accHooks(), page("c2", hookDoc(nil)), page("", hookDoc(map[string]any{"id": "whk_second"})))
	res = f.runAcc(WebhookLs, "--all")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, "whk_second") || strings.Contains(res.stderr, "more items") {
		t.Fatalf("--all: stdout %q, stderr %q", res.stdout, res.stderr)
	}
	if got := f.requests(); len(got) != 2 || got[1] != "GET "+accHooks()+"?cursor=c2" {
		t.Fatalf("calls %v", got)
	}
}

// TestWebhookShowPrintsTheEndpointAndItsRecentDeliveries.
func TestWebhookShowPrintsTheEndpointAndItsRecentDeliveries(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accHook(testHook), doc(200, hookDoc(map[string]any{"recent_deliveries": []map[string]any{deliveryDoc(nil)}})))
	res := f.runAcc(WebhookShow, testHook)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accHook(testHook))
	for _, want := range []string{"id=" + testHook + "\n", "description=orders\n", "state=active\n", "consecutive_failures=0\n",
		"last_success_at=2026-11-02T10:00:00Z\n", testDelivery} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.stdout)
		}
	}
	res = f.runAcc(WebhookShow, testHook, "--json")
	wantCode(t, res, 0)
	var got struct {
		ID               string           `json:"id"`
		RecentDeliveries []map[string]any `json:"recent_deliveries"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || got.ID != testHook || len(got.RecentDeliveries) != 1 {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}
}

// TestWebhookShowNotFoundIsExit4: a hook the server does not know.
func TestWebhookShowNotFoundIsExit4(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	res := f.runAcc(WebhookShow, testHook)
	wantCode(t, res, 4)
	res = f.runAcc(WebhookShow, "whk_short")
	wantCode(t, res, 2)
	res = f.runAcc(WebhookShow)
	wantCode(t, res, 2)
}

// TestWebhookUpdateSendsOnlyWhatWasGiven: each flag that is given is sent, and none that is not.
func TestWebhookUpdateSendsOnlyWhatWasGiven(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accHook(testHook), doc(200, hookDoc(map[string]any{"state": "paused"})))
	res := f.runAcc(WebhookUpdate, testHook, "--state", "paused")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accHook(testHook))
	if got := f.rawBody(0); got != `{"state":"paused"}` {
		t.Fatalf("body %s", got)
	}
	if !strings.Contains(res.stdout, "state=paused\n") {
		t.Fatalf("stdout %q", res.stdout)
	}

	f = newFake(t)
	f.on("PATCH", accHook(testHook), doc(200, hookDoc(nil)))
	wantCode(t, f.runAcc(WebhookUpdate, testHook, "--url", "https://other.example.test/in", "--event", "file.created,file.moved",
		"--description", "new"), 0)
	body := f.bodyOf(0)
	if body["url"] != "https://other.example.test/in" || body["description"] != "new" || body["state"] != nil {
		t.Fatalf("body %v", body)
	}
	if evs, _ := json.Marshal(body["events"]); string(evs) != `["file.created","file.moved"]` {
		t.Fatalf("events %s", evs)
	}
}

// TestWebhookUpdateCanClearTheDescription: --description "" sends an empty description, which clears it.
func TestWebhookUpdateCanClearTheDescription(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accHook(testHook), doc(200, hookDoc(map[string]any{"description": nil})))
	wantCode(t, f.runAcc(WebhookUpdate, testHook, "--description", ""), 0)
	if got := f.rawBody(0); got != `{"description":""}` {
		t.Fatalf("body %s", got)
	}
}

// TestWebhookUpdateRefusesBadInputBeforeAnyRequest.
func TestWebhookUpdateRefusesBadInputBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"nothing to change": {testHook},
		"bad state":         {testHook, "--state", "disabled"},
		"empty state":       {testHook, "--state", ""},
		"bad event":         {testHook, "--event", "nope"},
		"userinfo":          {testHook, "--url", "https://u:pw12345@hooks.example.test/in"},
		"ftp":               {testHook, "--url", "ftp://hooks.example.test/in"},
		"empty url":         {testHook, "--url", ""},
		"no hook":           {"--state", "paused"},
		"bad hook":          {"whk_x", "--state", "paused"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			res := f.runAcc(WebhookUpdate, args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
			if strings.Contains(res.stderr, "pw12345") {
				t.Fatalf("the refusal repeats the password: %q", res.stderr)
			}
		})
	}
}

// TestWebhookUpdateShowsTheServersConflict: a disabled hook changes state only with a new url; the refusal is exit 3.
func TestWebhookUpdateShowsTheServersConflict(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accHook(testHook), apiErr(409, "conflict", "a disabled webhook changes state only with a new URL in the same request"))
	res := f.runAcc(WebhookUpdate, testHook, "--state", "active")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "conflict") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

// TestWebhookRmAsksFirst: at a terminal the person types the hook's id; a wrong answer sends nothing; --yes skips the
// question; with no terminal and no --yes the command refuses before any request.
func TestWebhookRmAsksFirst(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", accHook(testHook), done(204))
	wantCode(t, f.runAcc(WebhookRm, testHook), 2)
	f.wantNoCalls()

	typeAtTerminal("not-the-id\n")
	res := f.runAcc(WebhookRm, testHook)
	wantCode(t, res, 2)
	f.wantNoCalls()
	if !strings.Contains(res.stderr, "Type "+testHook) {
		t.Fatalf("the question does not name the hook: %q", res.stderr)
	}

	typeAtTerminal(testHook + "\n")
	res = f.runAcc(WebhookRm, testHook)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accHook(testHook))
	if res.stdout != "target="+testHook+"\nstatus=done\n" {
		t.Fatalf("stdout %q", res.stdout)
	}

	isolate(t, testAccountKey)
	f = newFake(t)
	f.on("DELETE", accHook(testHook), done(204))
	res = f.runAcc(WebhookRm, testHook, "--yes", "--json")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + accHook(testHook))
	if !strings.Contains(res.stdout, `"status": "done"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestWebhookRmNotFound is exit 4, and --yes never skips validation of the id.
func TestWebhookRmNotFound(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(WebhookRm, testHook, "--yes"), 4)
	f = newFake(t)
	wantCode(t, f.runAcc(WebhookRm, "whk_x", "--yes"), 2)
	f.wantNoCalls()
}

// TestWebhookRotateSecretAsksFirstAndShowsTheNewSecretOnce.
func TestWebhookRotateSecretAsksFirstAndShowsTheNewSecretOnce(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHook(testHook)+"/secret", doc(200, hookDoc(map[string]any{
		"secret": testSecret, "previous_secret_until": "2026-11-03T10:00:00Z"})))
	wantCode(t, f.runAcc(WebhookRotateSecret, testHook), 2)
	f.wantNoCalls()
	typeAtTerminal("wrong\n")
	wantCode(t, f.runAcc(WebhookRotateSecret, testHook), 2)
	f.wantNoCalls()

	typeAtTerminal(testHook + "\n")
	res := f.runAcc(WebhookRotateSecret, testHook)
	wantCode(t, res, 0)
	f.wantCalls("POST " + accHook(testHook) + "/secret")
	if strings.Count(res.stdout, testSecret) != 1 || !strings.Contains(res.stdout, "previous_secret_until=2026-11-03T10:00:00Z\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stderr, testSecret) || !strings.Contains(res.stderr, "shown only now") {
		t.Fatalf("stderr %q", res.stderr)
	}

	f = newFake(t)
	f.on("POST", accHook(testHook)+"/secret", doc(200, hookDoc(map[string]any{"secret": testSecret})))
	res = f.runAcc(WebhookRotateSecret, testHook, "--yes", "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || got["secret"] != testSecret {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}
}

// TestWebhookTestQueuesAnEvent.
func TestWebhookTestQueuesAnEvent(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHook(testHook)+"/test", doc(202, map[string]any{"event_id": "evt_abc"}))
	res := f.runAcc(WebhookTest, testHook)
	wantCode(t, res, 0)
	f.wantCalls("POST " + accHook(testHook) + "/test")
	if res.stdout != "event_id=evt_abc\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	res = f.runAcc(WebhookTest, testHook, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"event_id": "evt_abc"`) {
		t.Fatalf("stdout %q", res.stdout)
	}
	f.set("POST", accHook(testHook)+"/test", apiErrWith(409, "conflict", "only an active webhook gets a test event", "state"))
	wantCode(t, f.runAcc(WebhookTest, testHook), 3)
}

// TestWebhookDeliveriesListsTheLog: a table, --all, and the delivery's fields only.
func TestWebhookDeliveriesListsTheLog(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	route := accHook(testHook) + "/deliveries"
	f.on("GET", route, page("c2", deliveryDoc(nil)), page("", deliveryDoc(map[string]any{"id": "dlv_second", "state": "delivered", "last_code": 200, "last_error": nil})))
	res := f.runAcc(WebhookDeliveries, testHook)
	wantCode(t, res, 0)
	for _, want := range []string{"ID", "STATE", "TYPE", "ATTEMPTS", "CODE", testDelivery, "failed", "file.created", "500", "http_status"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.stdout)
		}
	}
	if !strings.Contains(res.stderr, "more items exist") {
		t.Fatalf("stderr %q", res.stderr)
	}
	f = newFake(t)
	f.on("GET", route, page("c2", deliveryDoc(nil)), page("", deliveryDoc(map[string]any{"id": "dlv_second", "state": "delivered", "last_code": 200, "last_error": nil})))
	res = f.runAcc(WebhookDeliveries, testHook, "--all", "--json")
	wantCode(t, res, 0)
	var got []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || len(got) != 2 || got[1]["id"] != "dlv_second" {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}
}

// TestWebhookRetryRequeuesOneDelivery.
func TestWebhookRetryRequeuesOneDelivery(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	route := accHook(testHook) + "/deliveries/" + testDelivery + "/retry"
	f.on("POST", route, doc(202, deliveryDoc(map[string]any{"state": "pending", "finished_at": nil})))
	res := f.runAcc(WebhookRetry, testHook, testDelivery)
	wantCode(t, res, 0)
	f.wantCalls("POST " + route)
	if !strings.Contains(res.stdout, "state=pending\n") || !strings.Contains(res.stdout, "id="+testDelivery+"\n") {
		t.Fatalf("stdout %q", res.stdout)
	}
	f.set("POST", route, apiErrWith(409, "conflict", "only a failed delivery is retried", "state"))
	wantCode(t, f.runAcc(WebhookRetry, testHook, testDelivery), 3)
	f.set("POST", route, apiErr(404, "not_found", "no such delivery"))
	wantCode(t, f.runAcc(WebhookRetry, testHook, testDelivery), 4)

	f = newFake(t)
	wantCode(t, f.runAcc(WebhookRetry, testHook), 2)
	wantCode(t, f.runAcc(WebhookRetry, testHook, "dlv_x"), 2)
	wantCode(t, f.runAcc(WebhookRetry, "whk_x", testDelivery), 2)
	f.wantNoCalls()
}

// TestEveryWebhookCommandSendsTheCredentialAndNoOtherNameThanItsRoute: the bearer is the profile's key.
func TestEveryWebhookCommandCarriesTheKey(t *testing.T) {
	for _, c := range webhookCommands() {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			res := f.runAcc(c.run, c.args...)
			if res.code == 2 {
				t.Fatalf("exit 2: %q", res.stderr)
			}
			if len(f.requests()) != 1 {
				t.Fatalf("calls %v, want one", f.requests())
			}
			f.wantBearer(testAccountKey)
		})
	}
}
