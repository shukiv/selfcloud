// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// Webhook endpoints (design sections 9 and 10, slice j1): the routes that register an endpoint, its
// events and its secret. Delivery is slices j2 to j4.
//
// Only the account key changes endpoints. An owner reads them, without the secret. A secret is shown in
// the answer to its creation and to its rotation, and nowhere else. It is stored encrypted, bound to its
// account, endpoint and column (internal/webhook).

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

const (
	webhookURLMax         = 2048
	webhookDescriptionMax = 200
	// secretGrace is how long the secret a rotation replaces still signs. Receivers verify with either
	// until then, so a rotation does not break a receiver that has not yet switched.
	secretGrace = 24 * time.Hour
	// webhookEndpointLimit is the most endpoints an account may hold. The count is read in the same write as
	// the insert (store.Write is one writer at a time), so concurrent creates cannot both pass it.
	webhookEndpointLimit = 100
)

// webhookJSON is an endpoint as the reads answer it. It never holds the secret.
type webhookJSON struct {
	ID                  string     `json:"id"`
	URL                 string     `json:"url"`
	Description         *string    `json:"description"`
	Events              []string   `json:"events"`
	State               string     `json:"state"`
	DisabledReason      *string    `json:"disabled_reason"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastFailureAt       *time.Time `json:"last_failure_at"`
	// PreviousSecretUntil is when the secret a rotation replaced stops signing. Null when no rotation is
	// in its grace period.
	PreviousSecretUntil *time.Time `json:"previous_secret_until"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// webhookShown is an answer that shows a secret: the endpoint and its secret, once.
type webhookShown struct {
	webhookJSON
	Secret string `json:"secret"`
}

// previousUntil is the end of the grace period while it runs, and nil once it has passed. It uses the
// comparison webhookSecrets makes, so the answer and the signing agree.
func previousUntil(e *store.WebhookEndpoint, now time.Time) *time.Time {
	if e.PreviousSecretEnc == nil || e.PreviousSecretUntil == nil || !now.Before(*e.PreviousSecretUntil) {
		return nil
	}
	return e.PreviousSecretUntil
}

func toWebhookJSON(e *store.WebhookEndpoint) webhookJSON {
	out := webhookJSON{ID: e.ID, URL: e.URL, Events: e.Events, State: e.State,
		ConsecutiveFailures: e.ConsecutiveFailures, LastSuccessAt: e.LastSuccessAt,
		LastFailureAt: e.LastFailureAt, PreviousSecretUntil: previousUntil(e, time.Now()),
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}
	if out.Events == nil {
		out.Events = []string{}
	}
	if e.Description != "" {
		out.Description = &e.Description
	}
	if e.DisabledReason != "" {
		out.DisabledReason = &e.DisabledReason
	}
	return out
}

// webhookFor is the endpoint as this caller may read it. Only the account key sees the full URL; an owner
// sees its origin, because the URL may hold a token in its path or its query.
func webhookFor(r *http.Request, e *store.WebhookEndpoint) webhookJSON {
	out := toWebhookJSON(e)
	if p := principalOf(r); p == nil || p.Kind != auth.KindAccountKey {
		out.URL = urlOrigin(e.URL)
	}
	return out
}

func webhookBinding(accountID, endpointID, field string) webhook.Binding {
	return webhook.Binding{AccountID: accountID, EndpointID: endpointID, Field: field}
}

// webhookSecrets returns the plain secrets an endpoint signs with at now: the current one, then the one a
// rotation replaced while its grace period lasts. A secret that will not open is an error.
func (s *Server) webhookSecrets(e *store.WebhookEndpoint, now time.Time) ([][]byte, error) {
	cur, err := s.webhookSeal.Open(e.SecretEnc, webhookBinding(e.AccountID, e.ID, webhook.FieldSecret))
	if err != nil {
		return nil, err
	}
	out := [][]byte{cur}
	if e.PreviousSecretEnc != nil && e.PreviousSecretUntil != nil && now.Before(*e.PreviousSecretUntil) {
		prev, err := s.webhookSeal.Open(e.PreviousSecretEnc, webhookBinding(e.AccountID, e.ID, webhook.FieldPrevious))
		if err != nil {
			return nil, err
		}
		out = append(out, prev)
	}
	return out, nil
}

// webhookWriter loads the account for a change. Only the account key makes one: an owner, an installation
// admin and a token are refused as forbidden, and another account's key gets not_found.
func (s *Server) webhookWriter(r *http.Request) (*store.Account, error) {
	acc, err := s.writableAccount(r)
	if err != nil {
		return nil, err
	}
	if p := principalOf(r); p == nil || p.Kind != auth.KindAccountKey {
		return nil, apierr.New(apierr.Forbidden, "only the account key manages webhooks")
	}
	return acc, nil
}

// webhookReader loads the account for a read: the account key, or an owner of it. Neither reads a secret.
func (s *Server) webhookReader(r *http.Request) (*store.Account, error) {
	acc, err := s.managedAccount(r)
	if err != nil {
		return nil, err
	}
	p := principalOf(r)
	if p.Kind == auth.KindAccountKey || (p.Kind == auth.KindUser && p.Role == auth.RoleOwner) {
		return acc, nil
	}
	return nil, apierr.New(apierr.Forbidden, "only the account key and an owner read webhooks")
}

// cleanWebhookURL checks an endpoint URL: ASCII, at most 2048 characters, HTTPS (HTTP only when the
// operator allows it), a host that is a DNS name or an IP literal, no user name, no fragment, and a
// valid port. A refusal never includes the URL, which may carry a token in its query.
func cleanWebhookURL(raw string, allowHTTP bool) (string, error) {
	bad := func(msg string) error { return apierr.New(apierr.InvalidRequest, "url "+msg) }
	if raw == "" {
		return "", bad("is required")
	}
	if len(raw) > webhookURLMax {
		return "", bad("is longer than 2048 characters")
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c <= 0x20 || c >= 0x7f {
			return "", bad("must be ASCII with no spaces or control characters")
		}
	}
	if strings.Contains(raw, "#") {
		return "", bad("must not carry a fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", bad("is not valid")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return "", bad("must use https")
		}
	default:
		return "", bad("must use https")
	}
	if u.User != nil {
		return "", bad("must not carry a user name or password")
	}
	if u.Opaque != "" || u.Host == "" {
		return "", bad("must name a host")
	}
	host := u.Hostname()
	if !parseHostLiteral(host) {
		return "", bad("must name a host by a DNS name or an IP address")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", bad("has a port that is not valid")
		}
	}
	return raw, nil
}

var dnsLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// parseHostLiteral reports whether host is an IP literal or a DNS name of letters, digits and hyphens. The last label
// must hold a letter: "127.1", "2130706433" and "0x7f.1" are numeric forms that some resolvers read as addresses, so
// they are not names (decision 28).
func parseHostLiteral(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	if !strings.ContainsFunc(labels[len(labels)-1], isASCIILetter) {
		return false
	}
	return len(host) <= 253
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// cleanEvents checks the events an endpoint wants: at least one, each in the fixed list. A repeat is
// kept once, and the list is returned sorted.
func cleanEvents(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, apierr.New(apierr.InvalidRequest, "events must name at least one event")
	}
	out := []string{}
	for _, ev := range in {
		if !webhook.ValidEvent(ev) {
			return nil, apierr.New(apierr.InvalidRequest, "events names an event type that is not in the list")
		}
		if !slices.Contains(out, ev) {
			out = append(out, ev)
		}
	}
	slices.Sort(out)
	return out, nil
}

// cleanWebhookDescription checks a description: at most 200 characters, no control characters. "" means none.
func cleanWebhookDescription(s string) (string, error) {
	if utf8.RuneCountInString(s) > webhookDescriptionMax {
		return "", apierr.New(apierr.InvalidRequest, "description is longer than 200 characters")
	}
	if err := checkText("description", s); err != nil {
		return "", err
	}
	return s, nil
}

// urlOrigin is the part of an endpoint URL that may be shown to anyone but the account key (webhook.Origin).
// Audit entries, an owner's reads, the worker's logs and last_error all use this one helper.
func urlOrigin(raw string) string { return webhook.Origin(raw) }

// errWebhookURLRefused is the answer to an endpoint URL whose host may not be connected to. It is one message for
// a denied address, a name that does not resolve and a lookup that times out, so it never says whether a name
// resolves or what it resolved to (decision 28).
func errWebhookURLRefused() error {
	return apierr.New(apierr.InvalidRequest, "url cannot be used: its host is not allowed")
}

// checkWebhookURL checks the host of an endpoint URL against the deny table, by the addresses it resolves to. It runs
// before any write, since a lookup can take up to RegisterTimeout.
func (s *Server) checkWebhookURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errWebhookURLRefused()
	}
	if err := s.webhookClient.CheckURL(ctx, u.Hostname()); err != nil {
		return errWebhookURLRefused()
	}
	return nil
}

// checkChangedWebhookURL checks a URL that an update sets, unless the endpoint already has it, so a change of its
// description does not wait on a lookup.
func (s *Server) checkChangedWebhookURL(ctx context.Context, accountID, endpointID, raw string) error {
	cur, err := s.store.WebhookEndpoint(ctx, accountID, endpointID)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("webhook")
	}
	if err != nil {
		return err
	}
	if cur.URL == raw {
		return nil
	}
	return s.checkWebhookURL(ctx, raw)
}

// webhookOf loads the endpoint named in the path, within the account.
func (s *Server) webhookOf(r *http.Request, tx *store.Tx, accountID string) (*store.WebhookEndpoint, error) {
	e, err := tx.WebhookEndpoint(r.Context(), accountID, r.PathValue("hook"))
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("webhook")
	}
	return e, err
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	var in struct {
		URL         string   `json:"url"`
		Events      []string `json:"events"`
		Description string   `json:"description"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	u, err := cleanWebhookURL(in.URL, s.cfg.Webhooks.AllowHTTP)
	if err != nil {
		return err
	}
	evs, err := cleanEvents(in.Events)
	if err != nil {
		return err
	}
	desc, err := cleanWebhookDescription(in.Description)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.checkWebhookURL(ctx, u); err != nil {
		return err
	}
	now := store.Now()
	e := &store.WebhookEndpoint{ID: id.New("whk"), AccountID: acc.ID, URL: u, Description: desc, Events: evs,
		State: store.WebhookActive, CreatedBy: principalOf(r).ID, CreatedAt: now, UpdatedAt: now}
	raw := webhook.NewSecret()
	if e.SecretEnc, err = s.webhookSeal.Seal(raw, webhookBinding(acc.ID, e.ID, webhook.FieldSecret)); err != nil {
		return err
	}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		n, err := tx.CountWebhookEndpoints(ctx, acc.ID)
		if err != nil {
			return err
		}
		if n >= webhookEndpointLimit {
			return apierr.New(apierr.Conflict, "the account holds the most webhook endpoints it may").
				With("reason", "limit")
		}
		if err := tx.InsertWebhookEndpoint(ctx, e); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.create", e.ID,
			map[string]any{"url": urlOrigin(u), "events": evs}))
	})
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/accounts/"+acc.ID+"/webhooks/"+e.ID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, webhookShown{toWebhookJSON(e), webhook.FormatSecret(raw)})
	return nil
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookReader(r)
	if err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	list, next, err := s.store.WebhookEndpoints(r.Context(), acc.ID, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[webhookJSON]{Items: []webhookJSON{}, NextCursor: next}
	for _, e := range list {
		out.Items = append(out.Items, webhookFor(r, e))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookReader(r)
	if err != nil {
		return err
	}
	e, err := s.store.WebhookEndpoint(r.Context(), acc.ID, r.PathValue("hook"))
	if errors.Is(err, store.ErrNotFound) {
		return notFound("webhook")
	}
	if err != nil {
		return err
	}
	recent, err := s.store.RecentWebhookDeliveries(r.Context(), acc.ID, e.ID, recentDeliveries)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, webhookDetail{webhookFor(r, e), deliveriesJSON(recent)})
	return nil
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	var in struct {
		URL         *string   `json:"url"`
		Events      *[]string `json:"events"`
		Description *string   `json:"description"`
		State       *string   `json:"state"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	var (
		newURL, newDesc string
		newEvents       []string
	)
	if in.URL != nil {
		if newURL, err = cleanWebhookURL(*in.URL, s.cfg.Webhooks.AllowHTTP); err != nil {
			return err
		}
	}
	if in.Events != nil {
		if newEvents, err = cleanEvents(*in.Events); err != nil {
			return err
		}
	}
	if in.Description != nil {
		if newDesc, err = cleanWebhookDescription(*in.Description); err != nil {
			return err
		}
	}
	want := ""
	if in.State != nil {
		want = *in.State
		if want != store.WebhookActive && want != store.WebhookPaused {
			return apierr.New(apierr.InvalidRequest, "state must be active or paused")
		}
	}
	ctx := r.Context()
	if in.URL != nil {
		if err := s.checkChangedWebhookURL(ctx, acc.ID, r.PathValue("hook"), newURL); err != nil {
			return err
		}
	}
	s.atWrite()
	var e *store.WebhookEndpoint
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := s.webhookOf(r, tx, acc.ID)
		if err != nil {
			return err
		}
		urlChanged := in.URL != nil && newURL != cur.URL
		// A disabled endpoint leaves its state only with a new URL in the same request. Its secret rotation is
		// the other way out, and that pauses it. A state change alone, to paused or to active, is refused.
		if want != "" && cur.State == store.WebhookDisabled && !urlChanged {
			return apierr.New(apierr.Conflict,
				"a disabled webhook changes state only with a new URL in the same request, or after its secret is rotated")
		}
		if urlChanged {
			cur.URL = newURL
			cur.ConsecutiveFailures = 0
			cur.DisabledReason = ""
			if cur.State == store.WebhookDisabled {
				cur.State = store.WebhookPaused
			}
		}
		if in.Events != nil {
			cur.Events = newEvents
		}
		if in.Description != nil {
			cur.Description = newDesc
		}
		if want != "" {
			cur.State = want
		}
		if cur.State != store.WebhookDisabled {
			cur.DisabledReason = ""
		}
		cur.UpdatedAt = store.Now()
		if err := tx.UpdateWebhookEndpoint(ctx, cur); err != nil {
			return err
		}
		e = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.update", cur.ID,
			map[string]any{"url": urlOrigin(cur.URL), "events": cur.Events, "state": cur.State}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toWebhookJSON(e))
	return nil
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := s.webhookOf(r, tx, acc.ID)
		if err != nil {
			return err
		}
		if err := tx.DeleteWebhookEndpoint(ctx, acc.ID, cur.ID); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.delete", cur.ID,
			map[string]any{"url": urlOrigin(cur.URL)}))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleRotateWebhookSecret makes a new secret and shows it once. The secret it replaces still signs for
// 24 hours, if it can still be read; only one such old secret is kept, so a second rotation ends the
// first. A disabled endpoint becomes paused: it is switched on by a separate change.
func (s *Server) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.webhookWriter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	raw := webhook.NewSecret()
	s.atWrite()
	var e *store.WebhookEndpoint
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		cur, err := s.webhookOf(r, tx, acc.ID)
		if err != nil {
			return err
		}
		now := store.Now()
		newEnc, err := s.webhookSeal.Seal(raw, webhookBinding(acc.ID, cur.ID, webhook.FieldSecret))
		if err != nil {
			return err
		}
		var prevEnc []byte
		var until *time.Time
		if old, err := s.webhookSeal.Open(cur.SecretEnc, webhookBinding(acc.ID, cur.ID, webhook.FieldSecret)); err == nil {
			if prevEnc, err = s.webhookSeal.Seal(old, webhookBinding(acc.ID, cur.ID, webhook.FieldPrevious)); err != nil {
				return err
			}
			end := now.Add(secretGrace)
			until = &end
		}
		cur.SecretEnc = newEnc
		cur.PreviousSecretEnc = prevEnc
		cur.PreviousSecretUntil = until
		if cur.State == store.WebhookDisabled {
			cur.State = store.WebhookPaused
			cur.DisabledReason = ""
			cur.ConsecutiveFailures = 0
		}
		cur.UpdatedAt = now
		if err := tx.UpdateWebhookEndpoint(ctx, cur); err != nil {
			return err
		}
		e = cur
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "webhook.secret", cur.ID,
			map[string]any{"url": urlOrigin(cur.URL)}))
	})
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, webhookShown{toWebhookJSON(e), webhook.FormatSecret(raw)})
	return nil
}
