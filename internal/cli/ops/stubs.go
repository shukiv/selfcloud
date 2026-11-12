// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import "time"

var jobPollWait = func(int) time.Duration { return 0 }
