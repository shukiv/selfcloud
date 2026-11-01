// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build race

package api

// raceEnabled says the race detector is on: real-time limits mean nothing then.
const raceEnabled = true
