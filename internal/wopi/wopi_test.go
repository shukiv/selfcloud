// SPDX-License-Identifier: AGPL-3.0-or-later

package wopi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/wopi"
	"github.com/shukiv/selfcloud/internal/wopi/wopitest"
)

// clock is a settable time for the discovery cache.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newClock() *clock { return &clock{now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)} }

func TestParseDocumentKeepsOnlyTheDiscoveryHostsActions(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), nil)
	doc, err := d.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Openable("docx", "edit") || !doc.Openable("DOCX", "view") {
		t.Error("docx should open in edit and view (extension matched case-insensitively)")
	}
	if doc.Openable("pdf", "edit") || !doc.Openable("pdf", "view") {
		t.Error("pdf is view only")
	}
	if doc.Openable("odt", "edit") {
		t.Error("an action whose URL is on another host must be dropped")
	}
	if doc.Openable("exe", "edit") {
		t.Error("a type Collabora does not list must not open")
	}
	src := "https://selfcloud.example/wopi/files/nod_1"
	got, ok := doc.EditorURL("docx", "edit", src)
	if !ok {
		t.Fatal("no editor URL for docx edit")
	}
	if strings.ContainsAny(got, "<>") {
		t.Errorf("placeholders must be removed from the editor URL, got %q", got)
	}
	want := col.Origin() + "/browser/42745e44dd/cool.html?WOPISrc=" + url.QueryEscape(src)
	if got != want {
		t.Errorf("editor URL = %q, want %q", got, want)
	}
	if _, ok := doc.EditorURL("odt", "edit", src); ok {
		t.Error("the dropped foreign action must not give a URL")
	}
}

// TestConvertibleFromIsTheEditTypesOnTheDiscoveryHost: live Collabora's discovery lists no convert-to action, so
// ConvertibleFrom reads the edit actions on the discovery host instead. A view-only type, an edit on another host, and an
// unlisted type are all not convertible (decision 37).
func TestConvertibleFromIsTheEditTypesOnTheDiscoveryHost(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	doc, err := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), nil).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !doc.ConvertibleFrom("docx") || !doc.ConvertibleFrom("XLSX") {
		t.Error("a type listed for edit on the discovery host must be convertible (extension case-insensitive)")
	}
	if doc.ConvertibleFrom("pdf") {
		t.Error("pdf is listed for view only; it must not be convertible")
	}
	if doc.ConvertibleFrom("odt") {
		t.Error("an edit whose URL is on another host must be dropped, so odt is not convertible")
	}
	if doc.ConvertibleFrom("exe") {
		t.Error("a type Collabora does not list must not be convertible")
	}
}

func TestParseDocumentRefusesUnusableKeys(t *testing.T) {
	t.Parallel()
	const base = `<wopi-discovery><net-zone name="external-https"></net-zone><proof-key %s/></wopi-discovery>`
	cases := map[string]string{
		"no proof key":          ``,
		"modulus without expo.": `modulus="AAAA"`,
		"tiny modulus":          `modulus="AQAB" exponent="AQAB"`,
		"exponent below three":  `modulus="` + strings.Repeat("A", 344) + `" exponent="AQ=="`,
		"not base64 in modulus": `modulus="!!!" exponent="AQAB"`,
		"not XML at all":        "",
	}
	for name, attrs := range cases {
		body := strings.Replace(base, "%s", attrs, 1)
		if name == "not XML at all" {
			body = "<not-xml"
		}
		if _, err := wopi.ParseDocument([]byte(body), "https://office.example/hosting/discovery"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestDiscoveryFetchIsBoundedAndCached(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	clk := newClock()
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), clk.Now)
	ctx := context.Background()
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	if h := col.Hits(); h != 1 {
		t.Errorf("two reads inside the refresh interval fetched %d times, want 1", h)
	}
	clk.Add(wopi.RefreshEvery)
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	if h := col.Hits(); h != 2 {
		t.Errorf("a read after RefreshEvery fetched %d times in all, want 2", h)
	}
}

func TestDiscoveryFailureKeepsTheCachedDocument(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	clk := newClock()
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), clk.Now)
	ctx := context.Background()
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	col.FailDiscovery(http.StatusInternalServerError)
	clk.Add(wopi.RefreshEvery + time.Second)
	doc, err := d.Get(ctx)
	if err != nil || doc == nil {
		t.Fatalf("a failed refresh must keep the old document; err = %v", err)
	}
	if !doc.Openable("docx", "edit") {
		t.Error("the kept document lost its actions")
	}
}

// TestStaleDiscoveryDuringAnOutageFetchesOncePerRetryGap covers a cache older than RefreshEvery while Collabora is
// down. Every read must not start a fetch: one fetch in RetryGap, and the stale document keeps answering proofs.
func TestStaleDiscoveryDuringAnOutageFetchesOncePerRetryGap(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	clk := newClock()
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), clk.Now)
	ctx := context.Background()
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	col.FailDiscovery(http.StatusServiceUnavailable)
	clk.Add(wopi.RefreshEvery + time.Second)
	const url = "https://wopi.example/wopi/files/x?access_token=t"
	ts := wopitest.Ticks(clk.Now())
	for i := 0; i < 5; i++ {
		doc, err := d.Get(ctx)
		if err != nil || doc == nil {
			t.Fatalf("read %d with a stale cache and Collabora down: err = %v", i+1, err)
		}
		if !doc.VerifyProof("t", url, ts, wopitest.SignWith(col.Current(), "t", url, ts), "") {
			t.Fatalf("read %d: the stale document no longer verifies a proof signed by the cached key", i+1)
		}
	}
	if h := col.Hits(); h != 2 {
		t.Errorf("fetches with five reads inside RetryGap = %d, want 2 (the first, then one in the gap)", h)
	}
	clk.Add(wopi.RetryGap)
	if _, err := d.Get(ctx); err != nil {
		t.Fatalf("after RetryGap the stale document must still answer: %v", err)
	}
	if h := col.Hits(); h != 3 {
		t.Errorf("fetches after RetryGap = %d, want 3", h)
	}
}

func TestDiscoveryWithoutACacheWaitsBeforeTryingAgain(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	clk := newClock()
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), clk.Now)
	ctx := context.Background()
	col.FailDiscovery(http.StatusServiceUnavailable)
	if _, err := d.Get(ctx); err == nil {
		t.Fatal("no document and a failed fetch: want an error")
	}
	if _, err := d.Get(ctx); err == nil {
		t.Fatal("a second read inside RetryGap: want an error")
	}
	if h := col.Hits(); h != 1 {
		t.Errorf("fetches inside RetryGap = %d, want 1", h)
	}
	col.ResumeDiscovery()
	clk.Add(wopi.RetryGap)
	if _, err := d.Get(ctx); err != nil {
		t.Fatalf("after RetryGap the fetch must run again: %v", err)
	}
}

func TestRetryIsRateLimited(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	clk := newClock()
	d := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), clk.Now)
	ctx := context.Background()
	if _, err := d.Get(ctx); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := d.Retry(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if h := col.Hits(); h != 1 {
		t.Errorf("five Retry calls inside RetryGap fetched %d times in all, want 1", h)
	}
}

func TestDiscoveryRefusesRedirects(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	// The fake redirects /redirect to the discovery path. The client trusts the fake, so only the refusal can stop
	// the fetch: the redirect target must not be contacted.
	d := wopi.NewDiscovery(col.Origin()+"/redirect", col.Client(), nil)
	if _, err := d.Get(context.Background()); err == nil {
		t.Error("a redirect of the discovery URL must not be followed")
	}
	if h := col.Hits(); h != 0 {
		t.Errorf("the redirect target was contacted %d times", h)
	}
}

func TestDiscoveryDocumentIsSizeBounded(t *testing.T) {
	t.Parallel()
	big := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", wopi.MaxDocumentBytes+10)))
	}))
	t.Cleanup(big.Close)
	d := wopi.NewDiscovery(big.URL+wopitest.DiscoveryPath, big.Client(), nil)
	if _, err := d.Get(context.Background()); err == nil {
		t.Error("a document over MaxDocumentBytes must be refused")
	}
}

func TestDiscoveryFetchHasATimeout(t *testing.T) {
	t.Parallel()
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	client := slow.Client()
	client.Timeout = 100 * time.Millisecond
	d := wopi.NewDiscovery(slow.URL+wopitest.DiscoveryPath, client, nil)
	start := time.Now()
	if _, err := d.Get(context.Background()); err == nil {
		t.Fatal("a fetch that never answers must fail")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the fetch took %v; the timeout did not apply", took)
	}
}

func TestVerifyProofCombinations(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	doc, err := col.Discovery().Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const token = "sct_abc.def"
	const reqURL = "https://selfcloud.example/wopi/files/nod_1?access_token=sct_abc.def"
	ts := wopitest.Ticks(time.Now())
	cur := col.Current()
	old := wopitest.SignWith(cur, token, reqURL, ts) // signed with the current key
	cases := []struct {
		name     string
		token    string
		url      string
		ts       int64
		proof    string
		proofOld string
		want     bool
	}{
		{"X-WOPI-Proof with the current key", token, reqURL, ts, old, "", true},
		{"X-WOPI-ProofOld with the current key", token, reqURL, ts, "", old, true},
		{"URL differing only in case (both sides are uppercased)", token, strings.ToLower(reqURL), ts, old, "", true},
		{"URL with a different query string", token, reqURL + "&x=1", ts, old, "", false},
		{"URL with a different path", token, strings.Replace(reqURL, "nod_1", "nod_2", 1), ts, old, "", false},
		{"a different access token", "sct_other", reqURL, ts, old, "", false},
		{"a different timestamp", token, reqURL, ts + 1, old, "", false},
		{"no proof at all", token, reqURL, ts, "", "", false},
		{"a proof that is not base64", token, reqURL, ts, "!!!", "", false},
		{"a proof from another key", token, reqURL, ts,
			wopitest.SignWith(wopitest.New(t).Current(), token, reqURL, ts), "", false},
	}
	for _, tc := range cases {
		if got := doc.VerifyProof(tc.token, tc.url, tc.ts, tc.proof, tc.proofOld); got != tc.want {
			t.Errorf("%s: VerifyProof = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestVerifyProofAcceptsTheOldKey(t *testing.T) {
	t.Parallel()
	col := wopitest.New(t)
	ctx := context.Background()
	signedWithOld := col.Current()
	col.Rotate(t)
	// The fresh document holds the rotated keys: the old current key is now the old key.
	fresh, err := wopi.NewDiscovery(col.DiscoveryURL(), col.Client(), nil).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const token = "sct_abc.def"
	const reqURL = "https://selfcloud.example/wopi/files/nod_1"
	ts := wopitest.Ticks(time.Now())
	byOld := wopitest.SignWith(signedWithOld, token, reqURL, ts)
	if !fresh.VerifyProof(token, reqURL, ts, byOld, "") {
		t.Error("a proof signed with the old key must be accepted through X-WOPI-Proof")
	}
	if fresh.VerifyProof(token, reqURL, ts, "", byOld) {
		t.Error("an old-key signature in X-WOPI-ProofOld alone is not one of the three combinations")
	}
	cur := wopitest.SignWith(col.Current(), token, reqURL, ts)
	if !fresh.VerifyProof(token, reqURL, ts, cur, "") {
		t.Error("the new current key must verify too")
	}
}

func TestTimestampWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		at   time.Time
		want bool
	}{
		"now":             {now, true},
		"19 minutes old":  {now.Add(-19 * time.Minute), true},
		"21 minutes old":  {now.Add(-21 * time.Minute), false},
		"4 minutes ahead": {now.Add(4 * time.Minute), true},
		"6 minutes ahead": {now.Add(6 * time.Minute), false},
	}
	for name, tc := range cases {
		ts := wopitest.Ticks(tc.at)
		if got := wopi.TimestampFresh(ts, now); got != tc.want {
			t.Errorf("%s: TimestampFresh = %v, want %v", name, got, tc.want)
		}
	}
	if wopi.TimestampFresh(0, now) {
		t.Error("a zero timestamp is far outside the window")
	}
}
