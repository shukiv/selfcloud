// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// rewrapFixture is a store with one account, and the links keys on disk at the default names: the current key and the
// previous one. Endpoints are written with the secrets of a chosen key, as a rotation of the links key leaves them.
type rewrapFixture struct {
	t      *testing.T
	st     *store.Store
	dbPath string
	cfg    *config.Config
	acc    string
	cur    []byte
	prev   []byte
	keys   string
	plain  map[string][]byte // endpoint ID to its plain secret, to check after a rewrap
}

func newRewrapFixture(t *testing.T) *rewrapFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rewrap.db")
	st, err := store.Open(ctx, store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &rewrapFixture{t: t, st: st, dbPath: dbPath, cur: bytes.Repeat([]byte{7}, 32), prev: bytes.Repeat([]byte{3}, 32),
		plain: map[string][]byte{}, keys: filepath.Join(dir, "keys")}
	if err := os.MkdirAll(f.keys, 0o700); err != nil {
		t.Fatal(err)
	}
	writeKey(t, f.currentPath(), f.cur)
	writeKey(t, f.previousPath(), f.prev)
	f.cfg = &config.Config{DataDir: dir}
	f.acc = id.New("acc")
	now := store.Now()
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(ctx, &store.Account{ID: f.acc, Name: "Rewrap", Status: store.AccountActive, CreatedAt: now, UpdatedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rewrapFixture) currentPath() string  { return filepath.Join(f.keys, keyFileName) }
func (f *rewrapFixture) previousPath() string { return filepath.Join(f.keys, prevKeyName) }

func writeKey(t *testing.T, path string, key []byte) {
	t.Helper()
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seal returns the blob of plain under key, bound to the row and column of an endpoint.
func (f *rewrapFixture) seal(key, plain []byte, endpoint, field string) []byte {
	f.t.Helper()
	s, err := webhook.NewSealer(key, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	blob, err := s.Seal(plain, webhookBinding(f.acc, endpoint, field))
	if err != nil {
		f.t.Fatal(err)
	}
	return blob
}

// endpoint writes an endpoint in state whose current secret is sealed under key. A grace copy, sealed under graceKey,
// is added when graceKey is set, and it is live until graceUntil.
func (f *rewrapFixture) endpoint(state, reason string, key []byte, graceKey []byte, graceUntil time.Time) string {
	f.t.Helper()
	ep := id.New("whk")
	plain := bytes.Repeat([]byte{byte(len(f.plain) + 1)}, 32)
	f.plain[ep] = plain
	now := store.Now()
	e := &store.WebhookEndpoint{ID: ep, AccountID: f.acc, URL: "https://hooks.example.com/in", State: state,
		DisabledReason: reason, SecretEnc: f.seal(key, plain, ep, webhook.FieldSecret), CreatedBy: "key_test",
		CreatedAt: now, UpdatedAt: now}
	if graceKey != nil {
		e.PreviousSecretEnc = f.seal(graceKey, plain, ep, webhook.FieldPrevious)
		e.PreviousSecretUntil = &graceUntil
	}
	if err := f.st.Write(context.Background(), func(tx *store.Tx) error { return tx.InsertWebhookEndpoint(context.Background(), e) }); err != nil {
		f.t.Fatal(err)
	}
	return ep
}

// get returns an endpoint as it is stored now.
func (f *rewrapFixture) get(ep string) *store.WebhookEndpoint {
	f.t.Helper()
	e, err := f.st.WebhookEndpoint(context.Background(), f.acc, ep)
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

// readable reports whether the stored current secret of ep opens under key alone, and to what.
func (f *rewrapFixture) readable(ep string, key []byte) bool {
	f.t.Helper()
	s, err := webhook.NewSealer(key, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	got, err := s.Open(f.get(ep).SecretEnc, webhookBinding(f.acc, ep, webhook.FieldSecret))
	return err == nil && bytes.Equal(got, f.plain[ep])
}

// auditCount runs a count query on the store's database file, as countOf does for a server.
func (f *rewrapFixture) auditCount(query string, args ...any) int {
	f.t.Helper()
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// rewrap runs the rewrap at now, with or without the disable.
func (f *rewrapFixture) rewrap(now time.Time, disable bool) RewrapCounts {
	f.t.Helper()
	c, err := RewrapWebhookSecrets(context.Background(), f.st, f.cfg, now, disable)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func TestRewrapMovesSecretsToTheCurrentKey(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	now := time.Now()
	a := f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	b := f.endpoint(store.WebhookActive, "", f.cur, nil, time.Time{})
	c := f.endpoint(store.WebhookPaused, "", f.prev, f.prev, now.Add(time.Hour))

	counts := f.rewrap(now, false)
	want := RewrapCounts{Endpoints: 3, Rewrapped: 3}
	if counts != want {
		t.Fatalf("counts %+v, want %+v", counts, want)
	}
	for _, ep := range []string{a, b, c} {
		if !f.readable(ep, f.cur) {
			t.Fatalf("endpoint %s: the current secret does not open under the current key", ep)
		}
	}
	if got := f.get(c); got.State != store.WebhookPaused {
		t.Fatalf("the paused endpoint changed to %s", got.State)
	}
	grace := f.get(c).PreviousSecretEnc
	s, _ := webhook.NewSealer(f.cur, nil)
	if plain, err := s.Open(grace, webhookBinding(f.acc, c, webhook.FieldPrevious)); err != nil || !bytes.Equal(plain, f.plain[c]) {
		t.Fatalf("the grace copy did not move to the current key: %v", err)
	}
}

func TestRewrapTwiceChangesNothing(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	f.rewrap(time.Now(), false)
	again := f.rewrap(time.Now(), false)
	if again.Rewrapped != 0 || again.Unreadable != 0 || again.Endpoints != 1 {
		t.Fatalf("second run %+v, want one endpoint and nothing changed", again)
	}
}

// TestRewrapWithoutThePreviousKeyDisables: with the disable asked for, a secret that only the previous key opens, and whose
// key file is gone, disables its endpoint with secret_unreadable. An endpoint disabled already keeps its reason, and an
// expired grace copy that nothing signs with is left alone.
func TestRewrapWithoutThePreviousKeyDisables(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	now := time.Now()
	active := f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	paused := f.endpoint(store.WebhookPaused, "", f.prev, nil, time.Time{})
	failing := f.endpoint(store.WebhookDisabled, store.WebhookDisabledFailing, f.prev, nil, time.Time{})
	fine := f.endpoint(store.WebhookActive, "", f.cur, nil, time.Time{})
	expired := f.endpoint(store.WebhookActive, "", f.cur, f.prev, now.Add(-time.Hour))
	if err := os.Remove(f.previousPath()); err != nil {
		t.Fatal(err)
	}

	counts := f.rewrap(now, true)
	want := RewrapCounts{Endpoints: 5, Unreadable: 3, Disabled: 2, Expired: 1}
	if counts != want {
		t.Fatalf("counts %+v, want %+v", counts, want)
	}
	if e := f.get(active); e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("active endpoint %s/%s, want disabled/secret_unreadable", e.State, e.DisabledReason)
	}
	if e := f.get(paused); e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("paused endpoint %s/%s, want disabled/secret_unreadable", e.State, e.DisabledReason)
	}
	if e := f.get(failing); e.DisabledReason != store.WebhookDisabledFailing {
		t.Fatalf("a disabled endpoint's reason changed to %s", e.DisabledReason)
	}
	if e := f.get(fine); e.State != store.WebhookActive {
		t.Fatalf("an endpoint with a readable secret changed to %s", e.State)
	}
	if e := f.get(expired); e.State != store.WebhookActive {
		t.Fatalf("an endpoint whose grace copy expired changed to %s", e.State)
	}
	if n := f.auditCount(`SELECT COUNT(*) FROM audit_log WHERE action = 'webhook.disabled' AND target = ?`, active); n != 1 {
		t.Fatalf("audit entries for the disable: %d, want 1", n)
	}
}

// TestRewrapConfiguredPreviousKeyMissingWritesNothing: a previous key file that the configuration names and that is not
// there is an error, as the server treats it, and the rewrap writes nothing.
func TestRewrapConfiguredPreviousKeyMissingWritesNothing(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	ep := f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	before := append([]byte(nil), f.get(ep).SecretEnc...)
	f.cfg.Links.PreviousKeyFile = filepath.Join(f.keys, "not-mounted.key")
	if _, err := RewrapWebhookSecrets(context.Background(), f.st, f.cfg, time.Now(), true); err == nil {
		t.Fatal("a configured previous key that is missing did not fail the rewrap")
	}
	if !bytes.Equal(before, f.get(ep).SecretEnc) || f.get(ep).State != store.WebhookActive {
		t.Fatal("the rewrap wrote something before it failed")
	}
}

// TestRewrapWithAWrongCurrentKeyReportsWithoutDisabling: a current key that is a valid key but the wrong one makes every
// secret unreadable. By default the rewrap reports the count and disables nothing.
func TestRewrapWithAWrongCurrentKeyReportsWithoutDisabling(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	a := f.endpoint(store.WebhookActive, "", f.cur, nil, time.Time{})
	p := f.endpoint(store.WebhookPaused, "", f.cur, nil, time.Time{})
	writeKey(t, f.currentPath(), bytes.Repeat([]byte{9}, 32))

	counts := f.rewrap(time.Now(), false)
	if counts.Unreadable != 2 || counts.Disabled != 0 {
		t.Fatalf("counts %+v, want 2 unreadable and nothing disabled", counts)
	}
	if f.get(a).State != store.WebhookActive || f.get(p).State != store.WebhookPaused {
		t.Fatal("a rewrap without the disable flag changed an endpoint's state")
	}
}

// TestRewrapDisablesUnreadableOnlyWhenAsked: with the disable flag, the same endpoints are disabled with secret_unreadable.
func TestRewrapDisablesUnreadableOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	a := f.endpoint(store.WebhookActive, "", f.cur, nil, time.Time{})
	writeKey(t, f.currentPath(), bytes.Repeat([]byte{9}, 32))
	counts := f.rewrap(time.Now(), true)
	if counts.Unreadable != 1 || counts.Disabled != 1 {
		t.Fatalf("counts %+v, want one unreadable and one disabled", counts)
	}
	if e := f.get(a); e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("endpoint %s/%s, want disabled/secret_unreadable", e.State, e.DisabledReason)
	}
}

// TestRewrapRestoresAnEndpointWhoseSecretOpensAgain: an endpoint disabled as secret_unreadable whose secret now opens goes
// to paused with its reason cleared. It does not send until its owner sets it active. One whose secret still does not
// open stays disabled.
func TestRewrapRestoresAnEndpointWhoseSecretOpensAgain(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	back := f.endpoint(store.WebhookDisabled, store.WebhookDisabledSecret, f.cur, nil, time.Time{})
	still := f.endpoint(store.WebhookDisabled, store.WebhookDisabledSecret, bytes.Repeat([]byte{9}, 32), nil, time.Time{})
	counts := f.rewrap(time.Now(), false)
	if counts.Restored != 1 {
		t.Fatalf("restored %d, want 1", counts.Restored)
	}
	if e := f.get(back); e.State != store.WebhookPaused || e.DisabledReason != "" {
		t.Fatalf("restored endpoint %s/%q, want paused with no reason", e.State, e.DisabledReason)
	}
	if e := f.get(still); e.State != store.WebhookDisabled || e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("an unreadable endpoint %s/%q, want it still disabled", e.State, e.DisabledReason)
	}
}

// TestRewrapNeedsTheCurrentKey: a current key that is missing is an error, and the rewrap does not make one, since a new
// key would make every stored secret unreadable.
func TestRewrapNeedsTheCurrentKey(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	f.endpoint(store.WebhookActive, "", f.cur, nil, time.Time{})
	if err := os.Remove(f.currentPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := RewrapWebhookSecrets(context.Background(), f.st, f.cfg, time.Now(), true); err == nil {
		t.Fatal("a rewrap without the current key succeeded")
	}
	if _, err := os.Stat(f.currentPath()); !os.IsNotExist(err) {
		t.Fatalf("the rewrap made a key file: %v", err)
	}
}

// TestRewrapDisablesASecretNeitherKeyOpens: a blob that no configured key opens is unreadable, and with the disable flag
// its endpoint is disabled.
func TestRewrapDisablesASecretNeitherKeyOpens(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	stranger := bytes.Repeat([]byte{9}, 32)
	ep := f.endpoint(store.WebhookActive, "", stranger, nil, time.Time{})
	counts := f.rewrap(time.Now(), true)
	if counts.Unreadable != 1 || counts.Disabled != 1 {
		t.Fatalf("counts %+v, want one unreadable and one disabled", counts)
	}
	if e := f.get(ep); e.DisabledReason != store.WebhookDisabledSecret {
		t.Fatalf("reason %q, want secret_unreadable", e.DisabledReason)
	}
}

// TestRewrapReachesEveryBatch: more endpoints than one batch are all read, and each is rewrapped once.
func TestRewrapReachesEveryBatch(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	const n = rewrapBatch*2 + 17
	for i := 0; i < n; i++ {
		f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	}
	counts := f.rewrap(time.Now(), false)
	if counts.Endpoints != n || counts.Rewrapped != n {
		t.Fatalf("counts %+v, want %d endpoints, each rewrapped once", counts, n)
	}
}

// TestRewrapOverAStaleBlobWritesNothing: a secret that a rotation replaced after the read is not overwritten with the
// older copy. The write names the blob it replaces.
func TestRewrapOverAStaleBlobWritesNothing(t *testing.T) {
	t.Parallel()
	f := newRewrapFixture(t)
	ep := f.endpoint(store.WebhookActive, "", f.prev, nil, time.Time{})
	stale := f.get(ep).SecretEnc
	fresh := f.seal(f.cur, f.plain[ep], ep, webhook.FieldSecret)
	if err := f.st.Write(context.Background(), func(tx *store.Tx) error {
		ok, err := tx.SetWebhookSecret(context.Background(), f.acc, ep, false, stale, fresh)
		if err != nil || !ok {
			return fmt.Errorf("the first write did not apply: %v %v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Write(context.Background(), func(tx *store.Tx) error {
		ok, err := tx.SetWebhookSecret(context.Background(), f.acc, ep, false, stale, f.seal(f.prev, f.plain[ep], ep, webhook.FieldSecret))
		if err != nil || ok {
			return fmt.Errorf("a write over a blob that was replaced applied: %v %v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !f.readable(ep, f.cur) {
		t.Fatal("the rotated secret was overwritten")
	}
}

// TestFreshKeyRefusalHoldsThroughNew: through api.New, a start that would make the default signing key while the database
// holds endpoints is refused every time, and leaves no key file and no endpoint changed. The operator's two ways forward
// are the earlier key file put back, after which the start proceeds, and a new install with no endpoint, which starts and
// makes its key.
func TestFreshKeyRefusalHoldsThroughNew(t *testing.T) {
	t.Parallel()
	var cfg *config.Config
	fx := newWebhookFixture(t, func(c *config.Config) { cfg = c })
	hook, _ := fx.create(t, nil)
	before := fx.endpoint(t, hook)
	keyPath := filepath.Join(cfg.DataDir, "keys", keyFileName)
	orig, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		_, err := New(Options{Config: cfg, Store: fx.s.store, Mailer: &mail.Recorder{}})
		if err == nil {
			t.Fatalf("start %d with the key missing and endpoints present was accepted", i+1)
		}
		if !strings.Contains(err.Error(), "new-signing-key") || !strings.Contains(err.Error(), "earlier key file") {
			t.Fatalf("start %d: the refusal does not name both ways forward: %v", i+1, err)
		}
		if _, statErr := os.Stat(keyPath); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("start %d left a key file behind: %v", i+1, statErr)
		}
	}
	after := fx.endpoint(t, hook)
	if after.State != before.State || !bytes.Equal(after.SecretEnc, before.SecretEnc) || after.DisabledReason != before.DisabledReason {
		t.Fatal("a refused start changed the endpoint")
	}

	// The earlier key file goes back: the start proceeds, with the key that sealed the secrets.
	if err := os.WriteFile(keyPath, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Config: cfg, Store: fx.s.store, Mailer: &mail.Recorder{}}); err != nil {
		t.Fatalf("a start with the earlier key restored was refused: %v", err)
	}
	if got, err := os.ReadFile(keyPath); err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("the existing key was touched: %v", err)
	}

	// A new install with no endpoint starts and makes its key.
	var freshCfg *config.Config
	newTestServerWith(t, &mail.Recorder{}, func(c *config.Config) { freshCfg = c })
	if _, err := os.Stat(filepath.Join(freshCfg.DataDir, "keys", keyFileName)); err != nil {
		t.Fatalf("a new install made no key: %v", err)
	}
}
