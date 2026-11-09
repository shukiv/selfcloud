// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// TestParityRefusesCarriageReturn: the table uses LF line ends only, as the web reader's does. A table with a CR at a
// line end is refused; encoding/csv would otherwise strip it without a word.
func TestParityRefusesCarriageReturn(t *testing.T) {
	crlf := strings.Join([]string{
		"kind\tkey\tcommand\tslice\treason",
		"route\tGET /api/v1/info\t-\tcli3\tplanned, cli3",
		"",
	}, "\r\n")
	rows, problems, err := parseParity(strings.NewReader(crlf))
	if err == nil && len(problems) == 0 {
		t.Fatalf("a CRLF table was accepted: %d rows, no problem", len(rows))
	}
}
