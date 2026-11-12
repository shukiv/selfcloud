// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

const (
	// defaultJobWait is how long --wait follows a job before it gives up. The job goes on running on the server.
	defaultJobWait = 30 * time.Minute
	// maxPollFailures is how many polls in a row may fail to reach the server before the wait gives up.
	maxPollFailures = 3
)

// jobView is a job as the commands print it.
type jobView struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Target      string  `json:"target"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	ErrorCode   string  `json:"error_code,omitempty"`
	ErrorReason string  `json:"error_reason,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	FinishedAt  *string `json:"finished_at"`
}

func (v jobView) pairs() []output.KV {
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "kind", Value: v.Kind},
		{Key: "target", Value: v.Target},
		{Key: "status", Value: v.Status},
		{Key: "error", Value: v.Error},
		{Key: "error_code", Value: v.ErrorCode},
		{Key: "error_reason", Value: v.ErrorReason},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
		{Key: "finished_at", Value: str(v.FinishedAt)},
	}
}

// readJob reads one job.
func readJob(c *command, cn *conn.Conn, job string) (jobView, error) {
	resp, err := files.Send(cn, http.MethodGet, "/api/v1/jobs/"+job, nil, nil)
	if err != nil {
		return jobView{}, err
	}
	var v jobView
	if err := decodeAs(resp.Body, &v, "a job"); err != nil {
		return v, err
	}
	if v.ID == "" {
		return v, errNotA("a job")
	}
	return v, nil
}

// JobShow is `selfcloud job show JOB [--wait [--wait-timeout DURATION]]`: the state of a job. Without --wait a failed job
// is data and the exit code is 0. With --wait the command follows the job to its end, and a failed job is exit 1.
func JobShow(args []string, stdout, stderr io.Writer) int {
	c := begin("job show", false, stdout, stderr)
	var wait bool
	var timeout time.Duration
	c.set.BoolVar(&wait, "wait", false, "follow the job until it has succeeded or failed")
	c.set.DurationVar(&timeout, "wait-timeout", defaultJobWait, "with --wait, give up after this long; the job goes on running")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("job show takes one job id"))
	}
	job, err := idArg("job", "job", pos[0])
	if err != nil {
		return c.fail(err)
	}
	switch {
	case flagGiven(c.set, "wait-timeout") && !wait:
		return c.fail(exitcode.Usagef("--wait-timeout goes with --wait"))
	case timeout <= 0:
		return c.fail(exitcode.Usagef("--wait-timeout must be longer than zero"))
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	v, err := readJob(c, cn, job)
	if wait && err == nil {
		v, err = followJob(c, cn, job, v, timeout)
	}
	if err != nil && v.ID == "" {
		return c.fail(err)
	}
	if perr := c.printJob(v); perr != nil {
		return c.fail(perr)
	}
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

func (c *command) printJob(v jobView) error {
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, v.pairs())
}

// followJob polls the job, with a growing wait, until it has succeeded or failed. It returns the last state it read. A
// failed job is an error that comes with its state, and so is a wait that ran out.
func followJob(c *command, cn *conn.Conn, job string, v jobView, limit time.Duration) (jobView, error) {
	deadline := time.Now().Add(limit)
	polls, failures := 0, 0
	for {
		switch v.Status {
		case "succeeded":
			return v, nil
		case "failed":
			return v, &exitcode.Error{Exit: exitcode.Failed, Name: "job_failed", Message: "the job " + job + " failed"}
		}
		if !time.Now().Before(deadline) {
			return v, &exitcode.Error{Exit: exitcode.Failed, Name: "timeout", Message: fmt.Sprintf(
				"the job %s is not done after %s; it goes on running, so run selfcloud job show %s later", job, limit, job)}
		}
		polls++
		time.Sleep(min(jobPollWait(polls), time.Until(deadline)))
		next, err := readJob(c, cn, job)
		var te *client.TransportError
		switch {
		case err == nil:
			v, failures = next, 0
		case errors.As(err, &te) && failures+1 < maxPollFailures:
			failures++
		default:
			return jobView{}, err
		}
	}
}
