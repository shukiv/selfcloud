// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func jobRoute() string { return "/api/v1/jobs/" + testJob }

// jobDoc returns a job as the API answers it.
func jobDoc(status string, extra map[string]any) map[string]any {
	d := map[string]any{
		"id": testJob, "kind": "zip", "target": "zip_abc", "status": status,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:00:05Z", "finished_at": nil,
	}
	if status == "succeeded" || status == "failed" {
		d["finished_at"] = "2026-11-01T10:00:09Z"
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// TestJobShowPrintsTheState: one read, key=value lines, and the API's document under --json.
func TestJobShowPrintsTheState(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("running", nil)))
	res := f.run(JobShow, testJob)
	wantCode(t, res, 0)
	f.wantCalls("GET " + jobRoute())
	f.wantBearer(testAccountKey)
	want := "id=" + testJob + "\nkind=zip\ntarget=zip_abc\nstatus=running\nerror=\nerror_code=\nerror_reason=\n" +
		"created_at=2026-11-01T10:00:00Z\nupdated_at=2026-11-01T10:00:05Z\nfinished_at=\n"
	if res.stdout != want {
		t.Fatalf("stdout:\n%q\nwant\n%q", res.stdout, want)
	}
	res = f.run(JobShow, testJob, "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil || got["status"] != "running" {
		t.Fatalf("--json: %v, %q", err, res.stdout)
	}
}

// TestJobShowOfAFailedJobExitsZeroWithoutWait: a job's failure is data until --wait makes it the command's result.
func TestJobShowOfAFailedJobExitsZeroWithoutWait(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("failed", map[string]any{"error": "no space", "error_code": "quota", "error_reason": "full"})))
	res := f.run(JobShow, testJob)
	wantCode(t, res, 0)
	for _, want := range []string{"status=failed\n", "error=no space\n", "error_code=quota\n", "error_reason=full\n"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.stdout)
		}
	}
}

// TestJobShowRefusesABadIDBeforeAnyRequest.
func TestJobShowRefusesABadIDBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{{}, {"job_short"}, {testImport}, {testJob, "extra"}, {testJob + "/x"}} {
		isolate(t, testAccountKey)
		f := newFake(t)
		wantCode(t, f.run(JobShow, args...), 2)
		f.wantNoCalls()
	}
}

// TestJobShowNotFoundAndRefusalsKeepTheirExitCodes.
func TestJobShowNotFoundAndRefusalsKeepTheirExitCodes(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	wantCode(t, f.run(JobShow, testJob), 4)
	if res := f.run(JobShow, testJob); strings.Contains(res.stderr, "hint") {
		t.Fatalf("a job route got the account hint: %q", res.stderr)
	}
	f.set("GET", jobRoute(), apiErr(403, "forbidden", "no"))
	wantCode(t, f.run(JobShow, testJob), 3)
}

// TestJobShowWaitFollowsTheJobToItsEnd: it polls until the job has succeeded, prints the last state, and exits 0.
func TestJobShowWaitFollowsTheJobToItsEnd(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("queued", nil)), doc(200, jobDoc("running", nil)), doc(200, jobDoc("succeeded", nil)))
	res := f.run(JobShow, testJob, "--wait")
	wantCode(t, res, 0)
	f.wantCalls("GET "+jobRoute(), "GET "+jobRoute(), "GET "+jobRoute())
	if !strings.Contains(res.stdout, "status=succeeded\n") || strings.Contains(res.stdout, "queued") {
		t.Fatalf("stdout %q: want the last state only", res.stdout)
	}
}

// TestJobShowWaitOnAFailedJobExitsOne: the state is printed, then the failure is the result.
func TestJobShowWaitOnAFailedJobExitsOne(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("running", nil)), doc(200, jobDoc("failed", map[string]any{"error": "no space"})))
	res := f.run(JobShow, testJob, "--wait")
	wantCode(t, res, 1)
	if !strings.Contains(res.stdout, "status=failed\n") || !strings.Contains(res.stderr, "job_failed") {
		t.Fatalf("stdout %q, stderr %q", res.stdout, res.stderr)
	}
	res = f.run(JobShow, testJob, "--wait", "--json")
	wantCode(t, res, 1)
	if !strings.HasPrefix(res.stderr, `{"error"`) || !strings.Contains(res.stdout, `"status": "failed"`) {
		t.Fatalf("--json: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

// TestJobShowWaitGivesUpAfterTheTimeout: a job that does not end is reported, and the job goes on.
func TestJobShowWaitGivesUpAfterTheTimeout(t *testing.T) {
	isolate(t, testAccountKey)
	jobPollWait = func(int) time.Duration { return 2 * time.Millisecond }
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("running", nil)))
	res := f.run(JobShow, testJob, "--wait", "--wait-timeout", "60ms")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "timeout") || !strings.Contains(res.stderr, testJob) {
		t.Fatalf("stderr %q", res.stderr)
	}
	if n := len(f.requests()); n < 2 || n > 40 {
		t.Fatalf("%d polls in 60ms at a 2ms wait", n)
	}
}

// TestJobShowWaitStopsOnARefusal: a poll that the server refuses ends the wait with that refusal.
func TestJobShowWaitStopsOnARefusal(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("running", nil)), apiErr(404, "not_found", "no such job"))
	wantCode(t, f.run(JobShow, testJob, "--wait"), 4)
	f = newFake(t)
	f.on("GET", jobRoute(), doc(200, jobDoc("running", nil)), apiErr(403, "forbidden", "no"))
	wantCode(t, f.run(JobShow, testJob, "--wait"), 3)
}

// TestJobShowWaitFlagsAreChecked: --wait-timeout goes with --wait and must be positive.
func TestJobShowWaitFlagsAreChecked(t *testing.T) {
	for _, args := range [][]string{
		{testJob, "--wait-timeout", "1m"}, {testJob, "--wait", "--wait-timeout", "0s"}, {testJob, "--wait", "--wait-timeout", "-1m"},
	} {
		isolate(t, testAccountKey)
		f := newFake(t)
		wantCode(t, f.run(JobShow, args...), 2)
		f.wantNoCalls()
	}
}

// TestJobPollWaitBacksOff: half a second, doubling, never more than five seconds.
func TestJobPollWaitBacksOff(t *testing.T) {
	// The tests replace the wait; the program's own is the one under test.
	real := defaultJobPollWait
	want := []time.Duration{500 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		try := i
		if got := real(try); got != w {
			t.Errorf("wait before poll %d = %s, want %s", try, got, w)
		}
	}
}
