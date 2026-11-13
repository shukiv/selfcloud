// SPDX-License-Identifier: AGPL-3.0-or-later

package exitcode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/shukiv/selfcloud/internal/cli/client"
)

// TestCodesAreTheDesignTable pins the numbers of decision 123. A change here is a change to the CLI's
// contract with scripts.
func TestCodesAreTheDesignTable(t *testing.T) {
	t.Parallel()
	want := []int{OK, Failed, Usage, Refused, NotFound, CheckFailed, UploadExpired, Partial}
	for i, got := range want {
		if got != i {
			t.Errorf("code %d = %d, want %d", i, got, i)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		code   string
		want   int
	}{
		{200, "", OK},
		{204, "", OK},
		{400, "invalid_request", Usage},
		{413, "too_large", Usage},
		{415, "unsupported_media_type", Usage},
		{416, "range_not_satisfiable", Usage},
		{422, "", Usage},
		{401, "unauthenticated", Refused},
		{403, "forbidden", Refused},
		{409, "conflict", Refused},
		{412, "precondition_failed", Refused},
		{429, "rate_limited", Refused},
		{410, "link_expired", NotFound},
		{410, "", NotFound},
		{410, "upload_expired", UploadExpired},
		{404, "not_found", NotFound},
		{460, "checksum_mismatch", CheckFailed},
		{500, "internal", Failed},
		{503, "unavailable", Failed},
		{302, "", Failed},
		{0, "", Failed},
	}
	for _, c := range cases {
		if got := ForAPI(c.status, c.code); got != c.want {
			t.Errorf("ForAPI(%d, %q) = %d, want %d", c.status, c.code, got, c.want)
		}
	}
}

// TestEveryCodeIsReached gives a test to each exit code, through the function a command's error goes to.
func TestEveryCodeIsReached(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
		json string
	}{
		{"done", nil, OK, ""},
		{"failed, a server error", &client.APIError{Status: 500, Code: "internal", Message: "x"}, Failed, "internal"},
		{"failed, no server", &client.TransportError{Kind: client.KindUnreachable, Message: "x"}, Failed, "unreachable"},
		{"usage", Usagef("bad flag"), Usage, "usage"},
		{"refused by the server", &client.APIError{Status: 403, Code: "forbidden", Message: "x"}, Refused, "forbidden"},
		{"not found", NotFoundf("no profile"), NotFound, "not_found"},
		{"a check failed", &client.APIError{Status: 460, Code: "checksum_mismatch", Message: "x"}, CheckFailed, "checksum_mismatch"},
		{"an upload expired", &client.APIError{Status: 410, Code: "upload_expired", Message: "x"}, UploadExpired, "upload_expired"},
		{"partial success", Partialf("1 of 2 failed"), Partial, "partial"},
	}
	for _, c := range cases {
		got, name, _ := Of(c.err)
		if got != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, got, c.want)
		}
		if c.json != "" && name != c.json {
			t.Errorf("%s: JSON code = %q, want %q", c.name, name, c.json)
		}
	}
}

func TestUnknownHTTPStatusGetsACode(t *testing.T) {
	t.Parallel()
	_, name, _ := Of(&client.APIError{Status: 418})
	if name != "http_418" {
		t.Errorf("JSON code = %q, want http_418", name)
	}
}

func TestOfWrapsAndDoesNotInventAMessage(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("while reading: %w", Usagef("the name is not valid"))
	code, name, msg := Of(wrapped)
	if code != Usage || name != "usage" || msg != "while reading: the name is not valid" {
		t.Errorf("Of = %d %q %q", code, name, msg)
	}
	code, _, _ = Of(errors.New("disk on fire"))
	if code != Failed {
		t.Errorf("a plain error exit = %d, want %d", code, Failed)
	}
}
