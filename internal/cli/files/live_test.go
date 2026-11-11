// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// The large-file commands are proved against the real API on a temporary store, served on loopback: the tus routes,
// the ranged content route and the share-link routes are the server's own, with their own expiry and checksum rules.
// Two addresses serve the one server. Fixtures go to plain, with no fault and no record. The command under test goes to
// url, through a record of every call and, when the test gives one, a listener that cuts connections.

// mib is one mebibyte.
const mib = 1 << 20

// live is a real server with an account, its key and a team folder.
type live struct {
	t      *testing.T
	s      *api.Server
	st     *store.Store
	plain  string
	url    string
	rec    *callLog
	acc    string
	key    string
	folder string
	logs   *syncBuffer
}

// newLive starts the server. mutate changes the configuration before it is checked; cut, when not nil, is the listener
// the command's address goes through.
func newLive(t *testing.T, mutate func(*config.Config), cut *cutter) *live {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.Storage.MinFree = 0
	cfg.Store.Path = filepath.Join(dir, "selfcloud.db")
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: cfg.Store.Path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logs := &syncBuffer{}
	s, err := api.New(api.Options{Config: cfg, Store: st,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}

	now := store.Now()
	acc := &store.Account{ID: id.New("acc"), Name: "transfers", Status: store.AccountActive, CreatedAt: now, UpdatedAt: now}
	secret, prefix, hash := auth.NewKey(auth.AccountKeyPrefix)
	key := &store.APIKey{ID: id.New("key"), AccountID: acc.ID, Name: "test", Prefix: prefix, Hash: hash, CreatedAt: now}
	f := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "transfers", NameKey: "transfers"}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		if err := tx.InsertAPIKey(ctx, key); err != nil {
			return err
		}
		return tx.InsertFolder(ctx, f, "")
	}); err != nil {
		t.Fatal(err)
	}

	plain := httptest.NewServer(s.Handler())
	t.Cleanup(plain.Close)
	rec := &callLog{next: s.Handler()}
	srv := httptest.NewUnstartedServer(rec)
	if cut != nil {
		cut.Listener = srv.Listener
		srv.Listener = cut
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return &live{t: t, s: s, st: st, plain: plain.URL, url: srv.URL, rec: rec, acc: acc.ID, key: secret, folder: f.ID,
		logs: logs}
}

// isolateLive points the profiles at a temporary directory and makes the account key the credential the commands send.
func (l *live) isolate() {
	isolate(l.t, l.key)
}

// run calls a command against the address under test.
func (l *live) run(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	l.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", l.url), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// call makes one fixture request to plain as the account key, and returns the answer with its body read.
func (l *live) call(method, path string, header http.Header, body []byte) (*http.Response, []byte) {
	l.t.Helper()
	req, err := http.NewRequest(method, l.plain+path, bytes.NewReader(body))
	if err != nil {
		l.t.Fatal(err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	req.Header.Set("Authorization", "Bearer "+l.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		l.t.Fatal(err)
	}
	return resp, data
}

// jsonCall is call with a JSON body, wanting a 2xx answer.
func (l *live) jsonCall(method, path string, doc any) []byte {
	l.t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		l.t.Fatal(err)
	}
	resp, data := l.call(method, path, http.Header{"Content-Type": {"application/json"}}, b)
	if resp.StatusCode/100 != 2 {
		l.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	return data
}

// upload makes a file of data at name, in the directory dir (a path) of the folder, in one tus creation request, and
// returns its node id. onConflict is the API's: fail, rename or replace.
func (l *live) upload(dir, name string, data []byte, onConflict string) string {
	l.t.Helper()
	meta := []string{"folder " + b64(l.folder), "name " + b64(name), "on_conflict " + b64(onConflict)}
	if dir != "/" {
		meta = append(meta, "path "+b64(dir), "parents "+b64("true"))
	}
	h := http.Header{"Tus-Resumable": {"1.0.0"}, "Upload-Length": {strconv.Itoa(len(data))},
		"Upload-Metadata": {strings.Join(meta, ",")}, "Content-Type": {"application/offset+octet-stream"}}
	resp, body := l.call(http.MethodPost, "/api/v1/uploads", h, data)
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("Selfcloud-Node-ID") == "" {
		l.t.Fatalf("upload of %s: %d %s", name, resp.StatusCode, body)
	}
	return resp.Header.Get("Selfcloud-Node-ID")
}

// stat reads a node as the account key.
func (l *live) stat(nodeID string) nodeView {
	l.t.Helper()
	resp, body := l.call(http.MethodGet, "/api/v1/nodes/"+nodeID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("stat %s: %d %s", nodeID, resp.StatusCode, body)
	}
	v, err := decodeNode(body)
	if err != nil {
		l.t.Fatal(err)
	}
	return v
}

// content reads a file's bytes as the account key.
func (l *live) content(nodeID string) []byte {
	l.t.Helper()
	resp, body := l.call(http.MethodGet, "/api/v1/nodes/"+nodeID+"/content", nil, nil)
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("content of %s: %d %s", nodeID, resp.StatusCode, body)
	}
	return body
}

// child returns the node id of name in the folder's root, or "" when there is none.
func (l *live) child(name string) string {
	l.t.Helper()
	resp, body := l.call(http.MethodGet, "/api/v1/folders/"+l.folder+"/children?path=%2F", nil, nil)
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("children: %d %s", resp.StatusCode, body)
	}
	var page struct {
		Items []nodeView `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		l.t.Fatal(err)
	}
	for _, n := range page.Items {
		if n.Name == name {
			return n.ID
		}
	}
	return ""
}

// shareLink makes a link that anyone can open on nodeID, with password when it is not empty, and returns its URL on
// the address under test and the link's id. The account's switch for such links and the folder's allow_links are turned
// on first.
func (l *live) shareLink(nodeID, password string, extra map[string]any) (string, string) {
	l.t.Helper()
	l.jsonCall(http.MethodPatch, "/api/v1/accounts/"+l.acc+"/sharing-policy", map[string]any{"public_links": true})
	l.jsonCall(http.MethodPatch, "/api/v1/folders/"+l.folder+"/settings", map[string]any{"allow_links": true})
	body := map[string]any{"audience": "anyone"}
	if password != "" {
		body["password"] = password
	}
	for k, v := range extra {
		body[k] = v
	}
	var out struct {
		URL  string `json:"url"`
		Link struct {
			ID string `json:"id"`
		} `json:"link"`
	}
	if err := json.Unmarshal(l.jsonCall(http.MethodPost, "/api/v1/nodes/"+nodeID+"/share-links", body), &out); err != nil {
		l.t.Fatal(err)
	}
	_, secret, ok := strings.Cut(out.URL, "#")
	if !ok || secret == "" {
		l.t.Fatalf("the link's URL %q has no secret", out.URL)
	}
	return l.url + "/s#" + secret, out.Link.ID
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// randomBytes is n bytes from crypto/rand, so no two fixtures share content.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// sha is the hex SHA-256 of b.
func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// syncBuffer is a buffer the server's log writes to from many goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// seen is one call the record saw: what was asked, and what was answered.
type seen struct {
	method, path string
	auth         string
	offset       string // the Upload-Offset a piece was sent at
	checksum     string // the piece's Upload-Checksum
	length       int64  // the request's Content-Length
	rng, ifRange string // the Range and If-Range of a download
	status       int
	answerOffset string // the Upload-Offset of the answer
}

// callLog records every call to the address under test, in the order they arrive.
type callLog struct {
	mu    sync.Mutex
	calls []*seen
	next  http.Handler
}

func (l *callLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := &seen{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"),
		offset: r.Header.Get("Upload-Offset"), checksum: r.Header.Get("Upload-Checksum"), length: r.ContentLength,
		rng: r.Header.Get("Range"),
		ifRange: r.Header.Get("If-Range")}
	l.mu.Lock()
	l.calls = append(l.calls, c)
	l.mu.Unlock()
	cw := &captureWriter{ResponseWriter: w}
	l.next.ServeHTTP(cw, r)
	l.mu.Lock()
	c.status = cw.status
	c.answerOffset = cw.Header().Get("Upload-Offset")
	l.mu.Unlock()
}

// all returns copies of the calls seen so far.
func (l *callLog) all() []seen {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]seen, len(l.calls))
	for i, c := range l.calls {
		out[i] = *c
	}
	return out
}

// routes returns the calls as "METHOD /path".
func (l *callLog) routes() []string {
	var out []string
	for _, c := range l.all() {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

// captureWriter keeps the status a handler writes. Unwrap lets the server's response controller reach the connection.
type captureWriter struct {
	http.ResponseWriter
	status int
}

func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// cutter is the fault-injecting listener of design-cli section 6. It closes a connection after a set number of bytes
// has moved on it: read from the client, for an upload, or written to the client, for a download. The connections it
// accepts take the budgets in order; those past the list take after, where -1 is never cut and 0 is closed at once.
type cutter struct {
	net.Listener
	writes bool

	mu      sync.Mutex
	budgets []int64
	after   int64
	cuts    int
	onCut   func(cut int)
}

func newCutter(writes bool, after int64, budgets ...int64) *cutter {
	return &cutter{writes: writes, after: after, budgets: budgets}
}

// setAfter changes the budget of the connections past the list.
func (c *cutter) setAfter(n int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.after = n
}

// count is the number of connections cut so far.
func (c *cutter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cuts
}

func (c *cutter) Accept() (net.Conn, error) {
	conn, err := c.Listener.Accept()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	budget := c.after
	if len(c.budgets) > 0 {
		budget, c.budgets = c.budgets[0], c.budgets[1:]
	}
	c.mu.Unlock()
	if budget < 0 {
		return conn, nil
	}
	return &cutConn{Conn: conn, c: c, left: budget}, nil
}

// cutConn is one connection with its budget.
type cutConn struct {
	net.Conn
	c *cutter

	mu   sync.Mutex
	left int64
	done bool
}

// cut closes the connection once, and counts it.
func (k *cutConn) cut() {
	k.mu.Lock()
	if k.done {
		k.mu.Unlock()
		return
	}
	k.done = true
	k.mu.Unlock()
	k.c.mu.Lock()
	k.c.cuts++
	n, hook := k.c.cuts, k.c.onCut
	k.c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	_ = k.Conn.Close()
}

func (k *cutConn) Read(p []byte) (int, error) {
	if k.c.writes {
		return k.Conn.Read(p)
	}
	k.mu.Lock()
	left := k.left
	k.mu.Unlock()
	if left <= 0 {
		k.cut()
		return 0, io.EOF
	}
	if int64(len(p)) > left {
		p = p[:left]
	}
	n, err := k.Conn.Read(p)
	k.mu.Lock()
	k.left -= int64(n)
	left = k.left
	k.mu.Unlock()
	if left <= 0 {
		k.cut()
	}
	return n, err
}

func (k *cutConn) Write(p []byte) (int, error) {
	if !k.c.writes {
		return k.Conn.Write(p)
	}
	k.mu.Lock()
	left := k.left
	k.mu.Unlock()
	if left <= 0 {
		k.cut()
		return 0, net.ErrClosed
	}
	if int64(len(p)) <= left {
		n, err := k.Conn.Write(p)
		k.mu.Lock()
		k.left -= int64(n)
		k.mu.Unlock()
		return n, err
	}
	n, _ := k.Conn.Write(p[:left])
	k.mu.Lock()
	k.left = 0
	k.mu.Unlock()
	k.cut()
	return n, net.ErrClosed
}
