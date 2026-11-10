// SPDX-License-Identifier: AGPL-3.0-or-later

package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
)

func TestFailHumanLine(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	code := Fail(&errOut, false, &client.APIError{Status: 401, Code: "unauthenticated", Message: "the key is not valid"})
	if code != exitcode.Refused {
		t.Errorf("exit = %d, want %d", code, exitcode.Refused)
	}
	if got, want := errOut.String(), "selfcloud: unauthenticated: the key is not valid (HTTP 401)\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestFailJSONEnvelope: under --json an error is the API's envelope, on stderr, and nothing goes to stdout.
func TestFailJSONEnvelope(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	code := Fail(&errOut, true, exitcode.Usagef("the name is not valid"))
	if code != exitcode.Usage {
		t.Errorf("exit = %d, want %d", code, exitcode.Usage)
	}
	want := `{"error":{"code":"usage","message":"the name is not valid"}}` + "\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestFailNilIsDone(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	if code := Fail(&errOut, false, nil); code != 0 || errOut.Len() != 0 {
		t.Errorf("Fail(nil) = %d, stderr %q", code, errOut.String())
	}
}

func TestFailMessageWithControlCharactersIsOneLine(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	Fail(&errOut, false, errors.New("first\nsecond\tthird"))
	if strings.Count(errOut.String(), "\n") != 1 {
		t.Errorf("stderr = %q, want one line", errOut.String())
	}
}

func TestTableWithHeaderAndSanitizedCells(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := Table(&b, []string{"NAME", "URL"}, [][]string{{"home", "https://a.example"}, {"x\ty", "two\nlines"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[0], "NAME") || !strings.Contains(lines[0], "URL") {
		t.Errorf("header = %q", lines[0])
	}
	if strings.Count(b.String(), "\t") != 0 {
		t.Errorf("a tab survived into the table: %q", b.String())
	}
}

func TestKeyValues(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := KeyValues(&b, []KV{{"name", "SelfCloud"}, {"features", ""}}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "name=SelfCloud\nfeatures=\n"; got != want {
		t.Errorf("KeyValues = %q, want %q", got, want)
	}
}

func TestJSONIsIndentedWithANewline(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := JSON(&b, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "{\n  \"a\": 1\n}\n"; got != want {
		t.Errorf("JSON = %q, want %q", got, want)
	}
}
