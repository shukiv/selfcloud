// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

// SecretLine reads the first line of a secret from standard input, when Source is "-", or from a regular file. Limit is
// the longest line accepted, in bytes, and TrimSpace trims all the spaces around it (otherwise only the line ending).
// Each message is the usage error for one refusal, and TooLong is a format string for Limit. An error never repeats the
// secret or the file's name.
type SecretLine struct {
	Source     string
	Limit      int
	TrimSpace  bool
	Terminal   string
	NotExist   string
	OpenFailed string
	NotRegular string
	ReadFailed string
	TooLong    string
	Empty      string
}

// ReadSecretLine reads a secret line as o describes.
func ReadSecretLine(o SecretLine) (string, error) {
	var r io.Reader
	if o.Source == "-" {
		if IsTerminal(Stdin) {
			return "", exitcode.Usagef("%s", o.Terminal)
		}
		r = Stdin
	} else {
		f, err := os.Open(o.Source)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", exitcode.Usagef("%s", o.NotExist)
			}
			return "", exitcode.Usagef("%s", o.OpenFailed)
		}
		defer f.Close()
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			return "", exitcode.Usagef("%s", o.NotRegular)
		}
		r = f
	}
	line, err := bufio.NewReaderSize(io.LimitReader(r, int64(o.Limit)+2), o.Limit+2).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", exitcode.Usagef("%s", o.ReadFailed)
	}
	// A read that ran to its cap without reaching a line end stopped inside the line, so the limit was passed;
	// trimming what was read would keep a part of the line as if it were all of it.
	capped := len(line) >= o.Limit+2 && !strings.HasSuffix(line, "\n")
	if o.TrimSpace {
		line = strings.TrimSpace(line)
	} else {
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	}
	switch {
	case capped, len(line) > o.Limit:
		return "", exitcode.Usagef(o.TooLong, o.Limit)
	case line == "":
		return "", exitcode.Usagef("%s", o.Empty)
	}
	return line, nil
}
