// SPDX-License-Identifier: AGPL-3.0-or-later

package ops

import (
	"errors"
	"io"

	"github.com/shukiv/selfcloud/internal/cli/output"
)

func JobShow(args []string, stdout, stderr io.Writer) int {
	return output.Fail(stderr, false, errors.New("not built"))
}
