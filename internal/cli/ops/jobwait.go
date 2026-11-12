// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import "time"

// defaultJobPollWait is the wait before the try-th poll of a job that is still running: half a second, doubling, at most
// five seconds.
func defaultJobPollWait(try int) time.Duration {
	if try < 1 {
		try = 1
	}
	if try > 5 {
		return 5 * time.Second
	}
	return min(500*time.Millisecond<<(try-1), 5*time.Second)
}

// jobPollWait is the wait a command makes. Tests replace it.
var jobPollWait = defaultJobPollWait
