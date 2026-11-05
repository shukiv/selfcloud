// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "time"

// jsonBodyTimeout is how long a JSON request body may take to arrive (sec1, decision 84). A body that stalls is ended
// rather than held open. Tests shorten it.
var jsonBodyTimeout = 15 * time.Second
