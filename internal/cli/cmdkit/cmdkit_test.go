// SPDX-License-Identifier: AGPL-3.0-or-later

package cmdkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestNewAddsTheAccountFlagOnlyWhenAsked: --account belongs to the commands that name an account.
func TestNewAddsTheAccountFlagOnlyWhenAsked(t *testing.T) {
	var out, errOut bytes.Buffer
	with := New("with", true, &out, &errOut)
	if with.Set.Lookup("account") == nil {
		t.Error("New(withAccount=true) has no --account flag")
	}
	without := New("without", false, &out, &errOut)
	if without.Set.Lookup("account") != nil {
		t.Error("New(withAccount=false) has an --account flag")
	}
}

// TestAccountID: the flag is checked as an acc_ id, the environment is the default, and a command that names an account
// records it for the hints.
func TestAccountID(t *testing.T) {
	acc := id.New("acc")
	var out, errOut bytes.Buffer
	c := New("test", true, &out, &errOut)
	c.Account = acc
	got, err := c.AccountID()
	if err != nil || got != acc || !c.AccountUsed {
		t.Fatalf("AccountID() = %q, %v, AccountUsed=%v; want %q, nil, true", got, err, c.AccountUsed, acc)
	}

	c = New("test", true, &out, &errOut)
	t.Setenv("SELFCLOUD_ACCOUNT", acc)
	got, err = c.AccountID()
	if err != nil || got != acc {
		t.Fatalf("AccountID() from the environment = %q, %v; want %q, nil", got, err, acc)
	}

	c = New("test", true, &out, &errOut)
	c.Account = "not-an-account"
	_, err = c.AccountID()
	if err == nil || !strings.Contains(err.Error(), "an account is named by its acc_ id") {
		t.Fatalf("AccountID() with a bad id: err = %v", err)
	}
	if c.AccountUsed {
		t.Error("a refused account marked AccountUsed")
	}
}

// TestIDArg: the message names the id's prefix, and each package's article is the caller's.
func TestIDArg(t *testing.T) {
	usr := id.New("usr")
	got, err := IDArg("usr", "a user", usr)
	if err != nil || got != usr {
		t.Fatalf("IDArg() = %q, %v; want %q, nil", got, err, usr)
	}
	_, err = IDArg("usr", "a user", "usr_bad")
	if err == nil || err.Error() != "a user is named by its usr_ id" {
		t.Fatalf("IDArg() bad id: err = %v", err)
	}
	_, err = IDArg("grp", "the group", "grp_bad")
	if err == nil || err.Error() != "the group is named by its grp_ id" {
		t.Fatalf("IDArg() sharing article: err = %v", err)
	}
}

// TestNoArgs: positional arguments are refused with the command's name.
func TestNoArgs(t *testing.T) {
	if err := NoArgs("admin ls", nil); err != nil {
		t.Fatalf("NoArgs() = %v, want nil", err)
	}
	err := NoArgs("admin ls", []string{"extra"})
	if err == nil || err.Error() != "admin ls takes no arguments" {
		t.Fatalf("NoArgs() with an argument: err = %v", err)
	}
}

// TestFlagGiven: a flag set to its default is still given.
func TestFlagGiven(t *testing.T) {
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	var limit int
	set.IntVar(&limit, "limit", 3, "")
	if err := set.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if FlagGiven(set, "limit") {
		t.Error("FlagGiven() reported an unset flag")
	}
	if err := set.Parse([]string{"--limit", "3"}); err != nil {
		t.Fatal(err)
	}
	if !FlagGiven(set, "limit") {
		t.Error("FlagGiven() missed a flag set to its default")
	}
}

// TestDecodeAsAndDecodeItems: a bad document never repeats the server's answer.
func TestDecodeAsAndDecodeItems(t *testing.T) {
	var v struct {
		ID string `json:"id"`
	}
	if err := DecodeAs([]byte(`{"id":"one"}`), &v, "a thing"); err != nil || v.ID != "one" {
		t.Fatalf("DecodeAs() = %v, id=%q", err, v.ID)
	}
	err := DecodeAs([]byte(`not json`), &v, "a thing")
	if err == nil || err.Error() != "the server's answer is not a thing" {
		t.Fatalf("DecodeAs() bad body: err = %v", err)
	}
	if strings.Contains(err.Error(), "not json") {
		t.Error("DecodeAs() repeated the server's answer")
	}

	items, err := DecodeItems[struct {
		ID string `json:"id"`
	}]([]json.RawMessage{json.RawMessage(`{"id":"one"}`), json.RawMessage(`{"id":"two"}`)}, "a thing")
	if err != nil || len(items) != 2 || items[0].ID != "one" || items[1].ID != "two" {
		t.Fatalf("DecodeItems() = %+v, %v", items, err)
	}
	_, err = DecodeItems[any]([]json.RawMessage{json.RawMessage(`bad`)}, "a thing")
	if err == nil || err.Error() != "the server's answer is not a thing" {
		t.Fatalf("DecodeItems() bad item: err = %v", err)
	}
}

// TestPrintDone: the change names its target, as key=value lines or as JSON.
func TestPrintDone(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New("test", false, &out, &errOut)
	if err := PrintDone(c, "usr_one"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "target=usr_one") || !strings.Contains(out.String(), "status=done") {
		t.Errorf("plain output %q", out.String())
	}

	out.Reset()
	c.G.JSON = true
	if err := PrintDone(c, "usr_one"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("JSON output %q: %v", out.String(), err)
	}
	if doc["target"] != "usr_one" || doc["status"] != "done" {
		t.Errorf("JSON output %q", out.String())
	}
}

// TestPrintList: an empty plain list is a table with its headers, JSON keeps an empty list as [], and a further page is
// noted on standard error.
func TestPrintList(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New("test", false, &out, &errOut)
	if err := PrintList(c, []string{"one"}, true, []string{"NAME"}, func(v string) []string { return []string{v} }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "NAME") || !strings.Contains(out.String(), "one") {
		t.Errorf("plain output %q", out.String())
	}
	if !strings.Contains(errOut.String(), "more items exist") {
		t.Errorf("more note %q", errOut.String())
	}

	out.Reset()
	c.G.JSON = true
	if err := PrintList(c, []string(nil), false, []string{"NAME"}, func(v string) []string { return []string{v} }); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("empty JSON list %q, want []", out.String())
	}
}

// TestStrAndItoa: a nullable string and a decimal integer.
func TestStrAndItoa(t *testing.T) {
	s := "here"
	if got := Str(&s); got != "here" {
		t.Errorf("Str() = %q", got)
	}
	if got := Str(nil); got != "" {
		t.Errorf("Str(nil) = %q", got)
	}
	if got := Itoa(42); got != "42" {
		t.Errorf("Itoa() = %q", got)
	}
}
