// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func quotaRouteTest() string { return "/api/v1/accounts/" + testAcc + "/quota-warning" }

type quotaCommand struct {
	name string
	run  func([]string, io.Writer, io.Writer) int
	args []string
}

func quotaCommands() []quotaCommand {
	return []quotaCommand{
		{"account quota-warning show", AccountQuotaWarningShow, nil},
		{"account quota-warning set", AccountQuotaWarningSet, []string{"80"}},
		{"account quota-warning clear", AccountQuotaWarningClear, nil},
	}
}

// TestEveryQuotaCommandChecksItsAccountBeforeAnyRequest: with no account, a flag that is not an account id, or a variable
// that is not one, each command is refused with exit 2 and sends nothing.
func TestEveryQuotaCommandChecksItsAccountBeforeAnyRequest(t *testing.T) {
	for _, c := range quotaCommands() {
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

// TestQuotaWarningShowPrintsTheLevel: plain output is percent=N, and --json is {"percent":N}.
func TestQuotaWarningShowPrintsTheLevel(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", quotaRouteTest(), doc(200, map[string]any{"percent": 80}))
	res := f.runAcc(AccountQuotaWarningShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + quotaRouteTest())
	if res.stdout != "percent=80\n" {
		t.Errorf("stdout %q, want percent=80", res.stdout)
	}

	f = newFake(t)
	f.on("GET", quotaRouteTest(), doc(200, map[string]any{"percent": 80}))
	res = f.runAcc(AccountQuotaWarningShow, "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", res.stdout, err)
	}
	if len(got) != 1 || got["percent"] != float64(80) {
		t.Errorf("JSON %v, want {\"percent\":80}", got)
	}
}

// TestQuotaWarningSetSendsThePercentAndPrintsTheAnswer: the body holds a JSON number, and the answer is printed as show
// prints it.
func TestQuotaWarningSetSendsThePercentAndPrintsTheAnswer(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", quotaRouteTest(), doc(200, map[string]any{"percent": 80}))
	res := f.runAcc(AccountQuotaWarningSet, "80")
	wantCode(t, res, 0)
	f.wantCalls("PUT " + quotaRouteTest())
	if got := f.rawBody(0); got != `{"percent":80}` {
		t.Fatalf("body %q, want {\"percent\":80}", got)
	}
	if res.stdout != "percent=80\n" {
		t.Errorf("stdout %q, want percent=80", res.stdout)
	}

	f = newFake(t)
	f.on("PUT", quotaRouteTest(), doc(200, map[string]any{"percent": 80}))
	res = f.runAcc(AccountQuotaWarningSet, "80", "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", res.stdout, err)
	}
	if got["percent"] != float64(80) {
		t.Errorf("JSON %v, want percent 80", got)
	}
}

// TestQuotaWarningSetRefusesBadPercentsBeforeAnyRequest: only a whole number from 1 to 99 reaches the server.
func TestQuotaWarningSetRefusesBadPercentsBeforeAnyRequest(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"zero", []string{"0"}},
		{"hundred", []string{"100"}},
		{"negative", []string{"-5"}},
		{"fraction", []string{"80.5"}},
		{"text", []string{"abc"}},
		{"missing", nil},
		{"two percents", []string{"80", "90"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolate(t, testAccountKey)
			f := newFake(t)
			res := f.runAcc(AccountQuotaWarningSet, c.args...)
			wantCode(t, res, 2)
			// A value that starts with a minus is read as a flag, so the flag parser refuses it before the range is read.
			if (len(c.args) == 0 || !strings.HasPrefix(c.args[0], "-")) && !strings.Contains(res.stderr, "1 to 99") {
				t.Errorf("stderr %q does not name the range", res.stderr)
			}
			f.wantNoCalls()
		})
	}
}

// TestQuotaWarningClearDeletesWithoutAQuestionAndPrintsTheLevel: the answer is the level now in force.
func TestQuotaWarningClearDeletesWithoutAQuestionAndPrintsTheLevel(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("DELETE", quotaRouteTest(), doc(200, map[string]any{"percent": 75}))
	res := f.runAcc(AccountQuotaWarningClear)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + quotaRouteTest())
	if res.stdout != "percent=75\n" {
		t.Errorf("stdout %q, want percent=75", res.stdout)
	}

	f = newFake(t)
	f.on("DELETE", quotaRouteTest(), doc(200, map[string]any{"percent": 75}))
	res = f.runAcc(AccountQuotaWarningClear, "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not JSON: %v", res.stdout, err)
	}
	if got["percent"] != float64(75) {
		t.Errorf("JSON %v, want percent 75", got)
	}
}

// TestAQuotaAnswerThatIsNotTheLevelFails: a malformed answer is an error that names what was expected, with nothing on
// standard output.
func TestAQuotaAnswerThatIsNotTheLevelFails(t *testing.T) {
	for _, c := range quotaCommands() {
		for _, body := range []string{"[]", "not json"} {
			t.Run(c.name+" "+body, func(t *testing.T) {
				isolate(t, testAccountKey)
				f := newFake(t)
				method := "GET"
				if c.name != "account quota-warning show" {
					method = map[string]string{"account quota-warning set": "PUT", "account quota-warning clear": "DELETE"}[c.name]
				}
				f.on(method, quotaRouteTest(), reply{status: 200, body: body})
				res := f.runAcc(c.run, c.args...)
				wantCode(t, res, 1)
				if res.stdout != "" {
					t.Errorf("stdout %q, want empty", res.stdout)
				}
				if !strings.Contains(res.stderr, "quota warning level") {
					t.Errorf("stderr %q does not name the expectation", res.stderr)
				}
			})
		}
	}
}

// TestAQuotaRefusalDoesNotNameAnAdminKey: the quota routes are an account's, so the admin hint is not theirs.
func TestAQuotaRefusalDoesNotNameAnAdminKey(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on("GET", quotaRouteTest(), apiErr(403, "forbidden", "no"))
	res := f.runAcc(AccountQuotaWarningShow)
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, adminHintText) {
		t.Errorf("stderr %q holds the admin hint", res.stderr)
	}
}
