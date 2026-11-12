// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"errors"
	"io"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/output"
)

var jobPollWait = func(int) time.Duration { return 0 }

func notBuilt(args []string, stdout, stderr io.Writer) int {
	return output.Fail(stderr, false, errors.New("not built"))
}

func WebhookAdd(args []string, stdout, stderr io.Writer) int  { return notBuilt(args, stdout, stderr) }
func WebhookLs(args []string, stdout, stderr io.Writer) int   { return notBuilt(args, stdout, stderr) }
func WebhookShow(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func WebhookUpdate(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func WebhookRm(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func WebhookRotateSecret(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func WebhookTest(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
func WebhookDeliveries(args []string, stdout, stderr io.Writer) int {
	return notBuilt(args, stdout, stderr)
}
func WebhookRetry(args []string, stdout, stderr io.Writer) int { return notBuilt(args, stdout, stderr) }
