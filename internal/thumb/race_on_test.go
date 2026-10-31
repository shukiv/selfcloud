// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build race

package thumb

// underRace is true when the race detector is on. It maps shadow memory at start, which the child's relative
// address-space budget counts, so the memory-limit test cannot run under it.
const underRace = true
