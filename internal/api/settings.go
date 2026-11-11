// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// The settings API (decision 139). An installation admin, by key or by its own session, reads and changes the settings
// that are safe to change while the server runs. A key the config file or the environment writes is locked; the others
// are stored in the settings table. A change builds a new configuration, checks it with the file's rules and the
// settings' floors and caps, writes the table and the audit row in one transaction, and then swaps the snapshot every
// request reads, so the next request sees it.

// settings returns the configuration the server runs on now. The settings the API changes are read through it, once per
// request or per job run, never from s.cfg, which keeps the values of the start. The value is never nil and is never
// changed: a change swaps in a new one.
func (s *Server) settings() *config.Config { return s.snap.Load() }

// settingJSON is one setting in the settings answer. Value and default are a bool, an integer or a string, by type.
type settingJSON struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
	Restart bool   `json:"restart"`
}

// factJSON is a read-only fact of the configuration: infrastructure the config file owns, never a secret.
type factJSON struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// settingsJSON is the answer of GET and PATCH /api/v1/settings.
type settingsJSON struct {
	Settings []settingJSON `json:"settings"`
	Facts    []factJSON    `json:"facts"`
}

func settingsOf(c *config.Config) settingsJSON {
	out := settingsJSON{Settings: []settingJSON{}, Facts: []factJSON{}}
	for _, st := range c.Settings() {
		out.Settings = append(out.Settings, settingJSON{Key: st.Key, Type: string(st.Type), Value: st.Value,
			Default: st.Default, Source: string(st.Source), Locked: st.Locked, Restart: st.Restart})
	}
	for _, f := range c.Facts() {
		out.Facts = append(out.Facts, factJSON{Key: f.Key, Value: f.Value, Source: string(f.Source)})
	}
	return out
}

// handleGetSettings answers the settings and the read-only facts.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, settingsOf(s.settings()))
	return nil
}

// Limits of one change: no change needs more keys than there are settings, and no setting's name is long.
const (
	maxSettingsChange = 64
	maxSettingKey     = 128
)

// actionSettings is the audit action of a change to the settings. Its entry belongs to the installation (no account).
const actionSettings = "installation.settings"

// handlePatchSettings changes settings: set stores values, reset removes them so the defaults apply. All or nothing: an
// unknown key is a 400, a locked key a 409, and any invalid value a 400 that names every invalid key.
func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Set   map[string]json.RawMessage `json:"set"`
		Reset []string                   `json:"reset"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	keys, err := changeKeys(in.Set, in.Reset)
	if err != nil {
		return err
	}

	// One change at a time: each builds on the snapshot the one before it swapped in.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cur := s.settings()
	if err := cur.CheckKeys(keys); err != nil {
		return settingsError(err)
	}
	texts := map[string]string{}
	var problems []config.Problem
	for _, k := range slices.Sorted(maps.Keys(in.Set)) {
		text, err := config.SettingText(k, in.Set[k])
		if err != nil {
			problems = append(problems, config.Problem{Keys: []string{k}, Message: err.Error()})
			continue
		}
		texts[k] = text
	}
	next, err := cur.Change(texts, in.Reset)
	if err != nil || len(problems) > 0 {
		var ce *config.ChangeError
		if err != nil && !errors.As(err, &ce) {
			return err
		}
		if ce == nil {
			ce = &config.ChangeError{}
		}
		ce.Problems = append(problems, ce.Problems...)
		return settingsError(ce)
	}

	if changes := settingChanges(cur, next, keys); len(changes) > 0 {
		ctx := r.Context()
		err := s.store.Write(ctx, func(tx *store.Tx) error {
			for _, k := range keys {
				if text, ok := next.StoredText(k); ok {
					if err := tx.SetSetting(ctx, k, text); err != nil {
						return err
					}
				} else if err := tx.DeleteSetting(ctx, k); err != nil {
					return err
				}
			}
			return tx.InsertAudit(ctx, s.auditEntry(r, "", actionSettings, "settings", changes))
		})
		if err != nil {
			return err
		}
		s.snap.Store(next)
		s.settingsChanged(context.WithoutCancel(ctx), cur, next)
	}
	writeJSON(w, http.StatusOK, settingsOf(s.settings()))
	return nil
}

// changeKeys checks the shape of a change and returns its keys, sorted: at least one, at most maxSettingsChange, none
// longer than maxSettingKey, none reset twice and none both set and reset.
func changeKeys(set map[string]json.RawMessage, reset []string) ([]string, error) {
	n := len(set) + len(reset)
	if n == 0 {
		return nil, apierr.New(apierr.InvalidRequest, "set or reset at least one setting")
	}
	if n > maxSettingsChange {
		return nil, apierr.Newf(apierr.InvalidRequest, "a change may name at most %d settings", maxSettingsChange)
	}
	keys := slices.Collect(maps.Keys(set))
	for _, k := range reset {
		if _, ok := set[k]; ok || slices.Contains(keys, k) {
			return nil, apierr.New(apierr.InvalidRequest, "a setting is named twice; set it or reset it, once")
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		if len(k) > maxSettingKey {
			return nil, apierr.Newf(apierr.InvalidRequest, "a setting's name is longer than %d bytes", maxSettingKey)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

// settingsError turns a refused change into the API's error: unknown keys are a 400 and locked keys a 409, each listing
// its keys; invalid values are a 400 with every message, in the style of the config file's errors, and every key they
// name.
func settingsError(err error) error {
	var ce *config.ChangeError
	if !errors.As(err, &ce) {
		return err
	}
	switch {
	case len(ce.Unknown) > 0:
		return apierr.New(apierr.InvalidRequest, "not a setting: "+strings.Join(ce.Unknown, ", ")).
			With("reason", "unknown_setting").With("keys", ce.Unknown)
	case len(ce.Locked) > 0:
		return apierr.New(apierr.Conflict, "set in the server's config file or environment, or read only at start: "+
			strings.Join(ce.Locked, ", ")).With("reason", "setting_locked").With("keys", ce.Locked)
	}
	msgs := make([]string, len(ce.Problems))
	for i, p := range ce.Problems {
		msgs[i] = p.Message
	}
	return apierr.New(apierr.InvalidRequest, strings.Join(msgs, "; ")).
		With("reason", "invalid_setting").With("keys", ce.Keys()).With("errors", msgs)
}

// settingChanges returns the audit details of a change: for each key whose value or source changed, its old and new
// value as the table's text, and its new source. No setting the API changes is a secret.
func settingChanges(cur, next *config.Config, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		old, now := cur.ValueText(k), next.ValueText(k)
		if old == now && cur.Source(k) == next.Source(k) {
			continue
		}
		out[k] = map[string]any{"old": old, "new": now, "source": string(next.Source(k))}
	}
	return out
}

// settingsChanged does what a change needs beyond the swap. Turning search.office_text on with office on releases the
// documents skipped while it was off, as a start with it on does (Serve), so the content worker reads them.
func (s *Server) settingsChanged(ctx context.Context, cur, next *config.Config) {
	if next.Office.Enabled && next.Search.OfficeText && !cur.Search.OfficeText {
		if _, err := s.store.ReleaseTextSkipped(ctx); err != nil {
			s.log.Error("content search: cannot release the skipped documents", "error", err)
		}
	}
}
