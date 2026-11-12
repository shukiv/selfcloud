// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"io"
	"strings"
	"testing"
)

// The signing secret of a webhook is shown once, by add and by rotate-secret, and nowhere else. These tests are the rule,
// apart from the commands' own: every other command keeps an allow-list of fields, so a secret in an answer never reaches
// the terminal, and no error repeats a body.

// leaky returns extra fields that an answer must never carry to the terminal: a secret, an old secret, a signature, the
// delivery's headers and body.
func leaky() map[string]any {
	return map[string]any{
		"secret": testSecret, "previous_secret": testOldSecret, "secret_enc": "AAAA" + testSecret,
		"signature": "t=1,v1=" + testSecret, "headers": map[string]string{"Selfcloud-Signature": "t=1,v1=" + testSecret},
		"payload": `{"secret":"` + testSecret + `"}`, "body": testSecret,
		"signing_secret": testSecret, "secret_key": testSecret, "hmac": testSecret, "response_body": testSecret,
		"request_headers": map[string]string{"Selfcloud-Signature": "t=1,v1=" + testSecret, "Authorization": "Bearer " + testSecret},
	}
}

func noSecret(t *testing.T, what string, res result) {
	t.Helper()
	for _, s := range []string{testSecret, testOldSecret, "t=1,v1="} {
		if strings.Contains(res.stdout, s) || strings.Contains(res.stderr, s) {
			t.Fatalf("%s printed %q: stdout %q, stderr %q", what, s, res.stdout, res.stderr)
		}
	}
}

// TestNoCommandButAddAndRotatePrintsASecretTheServerSends: the server's answer carries a secret in every way a bug could
// put it; the commands that are not add or rotate-secret print none of it, plain or --json.
func TestNoCommandButAddAndRotatePrintsASecretTheServerSends(t *testing.T) {
	deliveries := accHook(testHook) + "/deliveries"
	cases := []struct {
		name  string
		run   func([]string, io.Writer, io.Writer) int
		args  []string
		setup func(f *fake)
	}{
		{"ls", WebhookLs, nil, func(f *fake) { f.on("GET", accHooks(), page("", hookDoc(leaky()))) }},
		{"show", WebhookShow, []string{testHook}, func(f *fake) {
			f.on("GET", accHook(testHook), doc(200, hookDoc(map[string]any{
				"secret": testSecret, "previous_secret": testOldSecret, "secret_enc": "AAAA" + testSecret,
				"recent_deliveries": []map[string]any{deliveryDoc(leaky())}})))
		}},
		{"update", WebhookUpdate, []string{testHook, "--state", "active"}, func(f *fake) {
			f.on("PATCH", accHook(testHook), doc(200, hookDoc(leaky())))
		}},
		{"test", WebhookTest, []string{testHook}, func(f *fake) {
			f.on("POST", accHook(testHook)+"/test", doc(202, map[string]any{"event_id": "evt_abc", "secret": testSecret}))
		}},
		{"job show", JobShow, []string{testJob}, func(f *fake) { f.on("GET", jobRoute(), doc(200, jobDoc("running", leaky()))) }},
		{"import show", ImportShow, []string{testImport}, func(f *fake) {
			f.on("GET", impRoute(testImport, ""), doc(200, importDoc(leaky())))
		}},
		{"import pause", ImportPause, []string{testImport}, func(f *fake) {
			f.on("POST", impRoute(testImport, "/pause"), doc(200, importDoc(leaky())))
		}},
		{"import items", ImportItems, []string{testImport}, func(f *fake) {
			f.on("GET", impRoute(testImport, "/items"), doc(200, map[string]any{"items": []map[string]any{itemDoc(1, leaky())}, "next": 1}))
		}},
		{"import archive set", ImportArchiveSet, []string{"on"}, func(f *fake) {
			f.on("PUT", archiveRoute(), doc(200, map[string]any{"source": "archive", "enabled": true, "signing_secret": testSecret}))
		}},
		{"deliveries", WebhookDeliveries, []string{testHook}, func(f *fake) { f.on("GET", deliveries, page("", deliveryDoc(leaky()))) }},
		{"retry", WebhookRetry, []string{testHook, testDelivery}, func(f *fake) {
			f.on("POST", deliveries+"/"+testDelivery+"/retry", doc(202, deliveryDoc(leaky())))
		}},
	}
	for _, c := range cases {
		for _, asJSON := range []bool{false, true} {
			name := c.name
			args := append([]string{}, c.args...)
			if asJSON {
				name += " --json"
				args = append(args, "--json")
			}
			t.Run(name, func(t *testing.T) {
				isolate(t, testAccountKey)
				f := newFake(t)
				c.setup(f)
				run := f.runAcc
				if strings.HasPrefix(c.name, "job ") || strings.HasPrefix(c.name, "import ") && c.name != "import archive set" {
					run = f.run // these routes name no account
				}
				res := run(c.run, args...)
				wantCode(t, res, 0)
				noSecret(t, name, res)
				if strings.Contains(res.stdout, "signature") || strings.Contains(res.stdout, "headers") {
					t.Fatalf("%s printed a field outside the allow-list: %q", name, res.stdout)
				}
			})
		}
	}
}

// TestTheSecretIsPrintedOnceByAddAndRotateAndByNothingElseAfter: the same fake answers add with a secret, then a show, an ls
// and a deliveries that (wrongly) carry it too; the secret appears in the add output only.
func TestTheSecretIsPrintedOnceByAddAndRotateAndByNothingElseAfter(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), doc(201, hookDoc(map[string]any{"secret": testSecret})))
	f.on("GET", accHook(testHook), doc(200, hookDoc(leaky())))
	f.on("GET", accHooks(), page("", hookDoc(leaky())))
	f.on("POST", accHook(testHook)+"/secret", doc(200, hookDoc(map[string]any{"secret": testOldSecret})))
	add := f.runAcc(WebhookAdd, "--endpoint", "https://hooks.example.test/in", "--event", "file.created")
	if strings.Count(add.stdout, testSecret) != 1 {
		t.Fatalf("add: %q", add.stdout)
	}
	for _, res := range []result{f.runAcc(WebhookShow, testHook), f.runAcc(WebhookLs), f.runAcc(WebhookShow, testHook, "--json")} {
		noSecret(t, "a read", res)
	}
	rot := f.runAcc(WebhookRotateSecret, testHook, "--yes")
	if strings.Count(rot.stdout, testOldSecret) != 1 || strings.Contains(rot.stdout, testSecret) {
		t.Fatalf("rotate: %q", rot.stdout)
	}
}

// TestNoErrorRepeatsAnAnswerBody: an answer that is not the document the command expects, or not JSON at all, is
// reported without its text, because the text may be a secret.
func TestNoErrorRepeatsAnAnswerBody(t *testing.T) {
	bad := []reply{
		{status: 201, body: testSecret},
		{status: 201, body: `{"id":1,"events":"` + testSecret + `","secret":"` + testSecret + `"}`},
		{status: 201, body: `["` + testSecret + `"]`},
		{status: 500, body: "internal " + testSecret},
		{status: 200, body: ``},
	}
	for _, rep := range bad {
		for _, asJSON := range []bool{false, true} {
			isolate(t, testAccountKey)
			f := newFake(t)
			f.on("POST", accHooks(), rep)
			f.on("POST", accHook(testHook)+"/secret", rep)
			f.on("GET", accHook(testHook), rep)
			args := []string{"--endpoint", "https://hooks.example.test/in", "--event", "file.created"}
			rotate := []string{testHook, "--yes"}
			show := []string{testHook}
			if asJSON {
				args, rotate, show = append(args, "--json"), append(rotate, "--json"), append(show, "--json")
			}
			for name, res := range map[string]result{
				"add": f.runAcc(WebhookAdd, args...), "rotate": f.runAcc(WebhookRotateSecret, rotate...), "show": f.runAcc(WebhookShow, show...),
			} {
				if res.code == 0 {
					t.Errorf("%s: exit 0 for status %d, which is not a webhook", name, rep.status)
				}
				noSecret(t, name, res)
			}
		}
	}
}

// TestWebhookAddPrintsNothingWhenTheAnswerHasNoSecret: an answer to add with no secret is a fault, not a success that
// leaves the person with a webhook they cannot verify.
func TestWebhookAddFailsWhenTheAnswerHasNoSecret(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("POST", accHooks(), doc(201, hookDoc(nil)))
	f.on("POST", accHook(testHook)+"/secret", doc(200, hookDoc(nil)))
	add := f.runAcc(WebhookAdd, "--endpoint", "https://hooks.example.test/in", "--event", "file.created")
	wantCode(t, add, 1)
	// The webhook exists now, so the error names it.
	if !strings.Contains(add.stderr, testHook) {
		t.Fatalf("stderr %q does not name the webhook that was made", add.stderr)
	}
	wantCode(t, f.runAcc(WebhookRotateSecret, testHook, "--yes"), 1)
}

// TestACredentialIsNeverPrinted: the key or token of the profile is in no output, whatever the command does.
func TestACredentialIsNeverPrinted(t *testing.T) {
	for _, tok := range []string{testAccountKey, testDeviceTok, testAdminKey} {
		for _, c := range webhookCommands() {
			isolate(t, tok)
			f := newFake(t)
			f.on("POST", accHooks(), doc(201, hookDoc(map[string]any{"secret": testSecret})))
			f.on("POST", accHook(testHook)+"/secret", doc(200, hookDoc(map[string]any{"secret": testSecret})))
			res := f.runAcc(c.run, c.args...)
			wantCode(t, res, res.code) // wantCode scans both streams for the three credentials
		}
	}
}
