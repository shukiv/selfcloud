// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func accEmbed() string { return "/api/v1/accounts/" + testAcc + "/embed" }

func embedDoc(origins ...string) map[string]any {
	if origins == nil {
		origins = []string{}
	}
	return map[string]any{"origins": origins}
}

func TestEmbedShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accEmbed(), doc(http.StatusOK, embedDoc("https://a.example", "https://b.example")))
	res := f.runAcc(EmbedShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accEmbed())
	f.wantBearer(testAccountKey)
	if res.stdout != "https://a.example\nhttps://b.example\n" {
		t.Fatalf("stdout %q, want one origin per line", res.stdout)
	}
	res = f.runAcc(EmbedShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"origins"`) || !strings.Contains(res.stdout, `"https://a.example"`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestEmbedShowEmpty(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accEmbed(), doc(http.StatusOK, embedDoc()))
	res := f.runAcc(EmbedShow)
	wantCode(t, res, 0)
	if res.stdout != "no sites may embed this account\n" {
		t.Fatalf("stdout %q, want the empty line", res.stdout)
	}
	res = f.runAcc(EmbedShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"origins": []`) {
		t.Fatalf("stdout %q is not the API's document", res.stdout)
	}
}

func TestEmbedShowJSONIsTheServersDocument(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	d := embedDoc("https://a.example")
	d["extra"] = "kept"
	f.on("GET", accEmbed(), doc(http.StatusOK, d))
	res := f.runAcc(EmbedShow, "--json")
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, `"extra": "kept"`) {
		t.Fatalf("stdout %q dropped a field of the server's document", res.stdout)
	}
	res = f.runAcc(EmbedShow)
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "extra") || strings.Contains(res.stdout, "kept") {
		t.Fatalf("stdout %q prints more than the origins", res.stdout)
	}
}

func TestEmbedShowRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(EmbedShow, "extra"), 2)
	wantCode(t, f.run(EmbedShow), 2)
	wantCode(t, f.run(EmbedShow, "--account", "acc_bad"), 2)
	f.wantNoCalls()
}

func TestEmbedShowNotFound(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accEmbed(), apiErr(http.StatusNotFound, "not_found", "no such account"))
	res := f.runAcc(EmbedShow)
	wantCode(t, res, 4)
	if !strings.Contains(res.stderr, "no such account") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestEmbedSetSendsTheOriginsAsGiven(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", accEmbed(), doc(http.StatusOK, embedDoc("https://a.example", "https://b.example")))
	res := f.runAcc(EmbedSet, "https://b.example", "https://a.example", "https://b.example")
	wantCode(t, res, 0)
	f.wantCalls("PUT " + accEmbed())
	f.wantBearer(testAccountKey)
	if got := f.rawBody(0); got != `{"origins":["https://b.example","https://a.example","https://b.example"]}` {
		t.Fatalf("body %q, want the origins in the order given, duplicates included", got)
	}
	if res.stdout != "https://a.example\nhttps://b.example\n" {
		t.Fatalf("stdout %q, want the list as embed show prints it", res.stdout)
	}
}

func TestEmbedSetTenOriginsAnd2048BytesReachTheServer(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = fmt.Sprintf("https://h%d.example", i)
	}
	f.on("PUT", accEmbed(), doc(http.StatusOK, embedDoc(ten...)))
	wantCode(t, f.runAcc(EmbedSet, ten...), 0)
	long := "https://" + strings.Repeat("a", 2040)
	f.on("PUT", accEmbed(), doc(http.StatusOK, embedDoc(long)))
	wantCode(t, f.runAcc(EmbedSet, long), 0)
	f.wantCalls("PUT "+accEmbed(), "PUT "+accEmbed())
}

func TestEmbedSetClearAsksFirst(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", accEmbed(), doc(http.StatusOK, embedDoc()))
	// no terminal and no --yes: refused before a request
	wantCode(t, f.runAcc(EmbedSet, "--clear"), 2)
	f.wantNoCalls()
	// a terminal where the account's id is typed
	typeAtTerminal(testAcc + "\n")
	res := f.runAcc(EmbedSet, "--clear")
	wantCode(t, res, 0)
	f.wantCalls("PUT " + accEmbed())
	if got := f.rawBody(0); got != `{"origins":[]}` {
		t.Fatalf("body %q, want an empty list", got)
	}
	if res.stdout != "no sites may embed this account\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
	// --yes skips the question
	res = f.runAcc(EmbedSet, "--clear", "--yes")
	wantCode(t, res, 0)
	f.wantCalls("PUT "+accEmbed(), "PUT "+accEmbed())
	if got := f.rawBody(1); got != `{"origins":[]}` {
		t.Fatalf("body %q, want an empty list", got)
	}
}

func TestEmbedSetRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("https://h%d.example", i)
	}
	for _, args := range [][]string{
		{},
		{"--clear", "https://a.example"},
		eleven,
		{""},
		{strings.Repeat("a", 2049)},
		{"a b"},
		{"a\tb"},
		{"a\nb"},
	} {
		wantCode(t, f.runAcc(EmbedSet, args...), 2)
	}
	wantCode(t, f.run(EmbedSet, "https://a.example"), 2)
	f.wantNoCalls()
}

func TestEmbedSetRefused(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PUT", accEmbed(), apiErr(http.StatusBadRequest, "invalid_request", `"ftp://x" is not an allowed origin`))
	res := f.runAcc(EmbedSet, "ftp://x")
	wantCode(t, res, 2)
	if !strings.Contains(res.stderr, "invalid_request") || !strings.Contains(res.stderr, "not an allowed origin") {
		t.Fatalf("stderr %q", res.stderr)
	}
}
