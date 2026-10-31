// SPDX-License-Identifier: AGPL-3.0-or-later

package office

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeConverter is a plain-HTTP stand-in for the conversion endpoint. Its handler decides each answer, and it records what
// it received.
type fakeConverter struct {
	srv           *httptest.Server
	hits          atomic.Int32
	mu            sync.Mutex
	path          string
	fileName      string
	contentLength int64 // the Content-Length the request arrived with
	chunked       bool  // the request was chunked
	received      int64 // the bytes the body held
	body          []byte
	inflight      int
	maxIn         int
	handle        func(w http.ResponseWriter, r *http.Request)
}

func newFakeConverter(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *fakeConverter {
	t.Helper()
	f := &fakeConverter{handle: handle}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		f.path = r.URL.Path
		f.inflight++
		f.maxIn = max(f.maxIn, f.inflight)
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.inflight--
			f.mu.Unlock()
		}()
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.contentLength, f.chunked, f.received = r.ContentLength, len(r.TransferEncoding) > 0, int64(len(raw))
		f.mu.Unlock()
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"]); mr != nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				if p.FormName() == "data" {
					f.mu.Lock()
					f.fileName, f.body = p.FileName(), b
					f.mu.Unlock()
				}
			}
		}
		handle(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// okPDF answers a valid PDF of n bytes.
func okPDF(n int) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), n)...))
	}
}

// newTestClient returns a client for the fake, and the discovery URL it was built from.
func newTestClient(t *testing.T, f *fakeConverter, o Options) *Client {
	t.Helper()
	c, err := New(f.srv.URL+"/hosting/discovery", nil, o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConvertSendsOnlyTheFixedNameAndFormat(t *testing.T) {
	f := newFakeConverter(t, okPDF(10))
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	var out bytes.Buffer
	err := c.Convert(context.Background(), strings.NewReader("doc bytes"), int64(len("doc bytes")), "docx", "pdf", &out)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if f.path != "/cool/convert-to/pdf" {
		t.Errorf("path %q, want /cool/convert-to/pdf", f.path)
	}
	if f.fileName != "source.docx" {
		t.Errorf("file name %q, want the fixed source.docx (the caller's name never reaches Collabora)", f.fileName)
	}
	if string(f.body) != "doc bytes" {
		t.Errorf("body %q, want the source bytes", f.body)
	}
	if !strings.HasPrefix(out.String(), "%PDF-") {
		t.Errorf("output does not start with the PDF signature")
	}
}

func TestConvertRefusesBadFormatsAndExtensionsBeforeSending(t *testing.T) {
	f := newFakeConverter(t, okPDF(10))
	c := newTestClient(t, f, Options{Timeout: time.Second, MaxOutput: 1 << 20})
	cases := []struct{ ext, format string }{
		{"docx", "exe"},
		{"docx", "../pdf"},
		{"docx", "PDF"},
		{"DOCX", "pdf"},
		{"../x", "pdf"},
		{"", "pdf"},
		{"toolongext", "pdf"},
	}
	for _, tc := range cases {
		err := c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), tc.ext, tc.format, io.Discard)
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("ext %q format %q: got %v, want ErrUnsupported", tc.ext, tc.format, err)
		}
	}
	if n := f.hits.Load(); n != 0 {
		t.Errorf("%d requests were sent for refused values, want none", n)
	}
}

func TestConvertClassifiesEveryAnswer(t *testing.T) {
	const secret = "SECRET-COLLABORA-TEXT-/home/x/letter.docx"
	cases := []struct {
		name    string
		handle  func(w http.ResponseWriter, r *http.Request)
		want    error
		maxOut  int64
		wantErr bool
	}{
		{"refused with its own text", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, secret, http.StatusBadRequest)
		}, ErrRefused, 1 << 20, true},
		{"overloaded", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, secret, http.StatusServiceUnavailable)
		}, ErrUnavailable, 1 << 20, true},
		// Live Collabora 26.04.5.2: a document the converter cannot save (a spreadsheet asked for as a text document, for
		// example) answers 500 with X-Error-Kind: savefailed and no body. That is a refusal, not an outage.
		{"cannot save the document (live: 500, savefailed)", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Error-Kind", "savefailed")
			w.WriteHeader(http.StatusInternalServerError)
		}, ErrRefused, 1 << 20, true},
		{"500 without a kind is also a refusal", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}, ErrRefused, 1 << 20, true},
		// Live Collabora 26.04.5.2 sends application/octet-stream with Content-Disposition for every format. The
		// magic bytes decide the file, not the type.
		{"live answer: octet-stream with the PDF signature", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="source.pdf"`)
			w.Write([]byte("%PDF-1.7\n"))
		}, nil, 1 << 20, false},
		{"octet-stream with HTML bytes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("<html>not a pdf</html>"))
		}, ErrBadOutput, 1 << 20, true},
		{"text instead of PDF", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("%PDF-1.4 but text"))
		}, ErrBadOutput, 1 << 20, true},
		{"PDF type with HTML bytes", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("<html>not a pdf</html>"))
		}, ErrBadOutput, 1 << 20, true},
		{"a body shorter than the signature", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%P"))
		}, ErrBadOutput, 1 << 20, true},
		{"output over the limit", okPDF(4096), ErrTooLarge, 1024, true},
		{"output exactly at the limit", okPDF(0), nil, 9, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeConverter(t, tc.handle)
			c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: tc.maxOut})
			err := c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard)
			if tc.wantErr {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "letter") {
					t.Errorf("the converter's text reached the error: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("got %v, want success", err)
			}
		})
	}
}

// TestConvertDoesNotFollowRedirects: an answer that moves the request elsewhere is refused, and the target is never reached.
func TestConvertDoesNotFollowRedirects(t *testing.T) {
	target := newFakeConverter(t, okPDF(10))
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.srv.URL+"/cool/convert-to/pdf", http.StatusFound)
	})
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	err := c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v, want ErrRefused for a redirect", err)
	}
	if n := target.hits.Load(); n != 0 {
		t.Errorf("the redirect target was reached %d times, want 0", n)
	}
}

// TestConvertHonoursItsTimeout: a converter that stalls is cut off, and the caller gets ErrUnavailable, not a hang.
func TestConvertHonoursItsTimeout(t *testing.T) {
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	c := newTestClient(t, f, Options{Timeout: 100 * time.Millisecond, MaxOutput: 1 << 20})
	start := time.Now()
	err := c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the conversion took %v, want the timeout to end it", d)
	}
}

// TestConvertLimitsConcurrency: with two slots, no more than two conversions are at the converter at once.
func TestConvertLimitsConcurrency(t *testing.T) {
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		okPDF(10)(w, r)
	})
	c := newTestClient(t, f, Options{Concurrent: 2, Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	f.mu.Lock()
	maxIn := f.maxIn
	f.mu.Unlock()
	if maxIn > 2 {
		t.Errorf("%d conversions at the converter at once, want at most 2", maxIn)
	}
	if maxIn < 2 {
		t.Errorf("only %d at once: the test did not overlap the requests", maxIn)
	}
}

// TestConvertWaitsForASlotOnlyAsLongAsItsContext: a caller that gives up while waiting for a slot sends nothing.
func TestConvertWaitsForASlotOnlyAsLongAsItsContext(t *testing.T) {
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		// Bounded, so the server can close: the held request ends at the client's own timeout.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	c := newTestClient(t, f, Options{Concurrent: 1, Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	held := make(chan struct{})
	go func() {
		close(held)
		_ = c.Convert(context.Background(), strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard)
	}()
	<-held
	for f.hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Convert(ctx, strings.NewReader("x"), int64(len("x")), "docx", "pdf", io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the caller's deadline", err)
	}
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d requests, want 1: the waiting caller sent none", n)
	}
}

func TestCapabilitiesAreCachedAndFailuresAreNotRepeated(t *testing.T) {
	fail := atomic.Bool{}
	fail.Store(true)
	f := newFakeConverter(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"convert-to":{"available":true},"productVersion":"24.04.7.2","productVersionHash":"c6c73bdf"}`))
	})
	c := newTestClient(t, f, Options{Timeout: 5 * time.Second, MaxOutput: 1 << 20})
	now := time.Now()
	c.now = func() time.Time { return now }

	if _, err := c.Capabilities(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first fetch: %v, want ErrUnavailable", err)
	}
	if _, err := c.Capabilities(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("second fetch: %v, want ErrUnavailable", err)
	}
	if n := f.hits.Load(); n != 1 {
		t.Errorf("%d fetches inside the retry gap, want 1", n)
	}
	fail.Store(false)
	now = now.Add(2 * time.Minute)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("after the gap: %v", err)
	}
	if !caps.Available || caps.VersionHash != "c6c73bdf" {
		t.Errorf("caps %+v, want available with the build hash", caps)
	}
	if _, err := c.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.hits.Load(); n != 2 {
		t.Errorf("%d fetches, want 2 (one failed, one good, then the hour's cache): the good answer is kept", n)
	}
}

func TestNewRequiresASchemeAndHost(t *testing.T) {
	for _, u := range []string{"", "/hosting/discovery", "office.example.com/hosting/discovery"} {
		if _, err := New(u, nil, Options{}); err == nil {
			t.Errorf("New(%q) accepted a URL with no scheme and host", u)
		}
	}
}

func TestConverterKeyChangesWithTheBuildAndTheLimits(t *testing.T) {
	a := Caps{Available: true, Version: "24.04.7.2", VersionHash: "c6c73bdf"}
	b := a
	b.VersionHash = "d28ff6e0"
	k := ConverterKey(a, "src=1 out=2")
	if k == ConverterKey(b, "src=1 out=2") {
		t.Error("a new build keeps the same converter key")
	}
	if k == ConverterKey(a, "src=1 out=3") {
		t.Error("new limits keep the same converter key")
	}
	if k != ConverterKey(a, "src=1 out=2") {
		t.Error("the key is not stable")
	}
	if len(k) > 64 {
		t.Errorf("key %q is longer than the 64 bytes the store allows", k)
	}
}
