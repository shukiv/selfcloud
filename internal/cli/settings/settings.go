// SPDX-License-Identifier: AGPL-3.0-or-later

// Package settings is `selfcloud settings`: the server's live settings, GET and PATCH /api/v1/settings, and the logo and
// the icon, PUT and DELETE /api/v1/settings/brand/{logo,icon}. The routes take an installation admin key.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

const (
	settingsPath = "/api/v1/settings"
	adminHint    = "settings needs an installation admin key (sc_admin_...); create one on the server with " +
		"selfcloud admin create-key"
)

// setting is one entry of the answer's settings. Value and Default are what json.Decoder.UseNumber gives: a bool, a
// json.Number, a string or nil, so an integer prints with every digit.
type setting struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
	Restart bool   `json:"restart"`
}

// picture is one entry of the answer's pictures.
type picture struct {
	Key    string `json:"key"`
	State  string `json:"state"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256"`
	Locked bool   `json:"locked"`
}

// answer is the document GET and PATCH answer, and the picture routes too. Raw keeps each entry as the server wrote it,
// for --json with one key.
type answer struct {
	Settings    []setting
	Pictures    []picture
	RawSettings []json.RawMessage
	RawPictures []json.RawMessage
}

// parseAnswer decodes the server's document. The facts are not read: only --json shows them.
func parseAnswer(body []byte) (*answer, error) {
	var doc struct {
		Settings []json.RawMessage `json:"settings"`
		Pictures []json.RawMessage `json:"pictures"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("the server's answer is not the settings document")
	}
	a := &answer{RawSettings: doc.Settings, RawPictures: doc.Pictures}
	for _, raw := range doc.Settings {
		var s setting
		if err := decodeNumber(raw, &s); err != nil {
			return nil, errors.New("the server's answer is not the settings document")
		}
		a.Settings = append(a.Settings, s)
	}
	for _, raw := range doc.Pictures {
		var p picture
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errors.New("the server's answer is not the settings document")
		}
		a.Pictures = append(a.Pictures, p)
	}
	return a, nil
}

// decodeNumber decodes raw into v with numbers kept as text, so a large integer is not rounded through a float.
func decodeNumber(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

// setting returns the entry for key, and its place.
func (a *answer) setting(key string) (setting, int, bool) {
	for i, s := range a.Settings {
		if s.Key == key {
			return s, i, true
		}
	}
	return setting{}, 0, false
}

// picture returns the entry for key, and its place.
func (a *answer) picture(key string) (picture, int, bool) {
	for i, p := range a.Pictures {
		if p.Key == key {
			return p, i, true
		}
	}
	return picture{}, 0, false
}

// valueText is a value as the server gave it: strings as they are, an empty string as "", a null as nothing.
func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if x == "" {
			return `""`
		}
		return x
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// note joins the flags of a setting for the NOTE column.
func note(locked, restart bool) string {
	var parts []string
	if locked {
		parts = append(parts, "locked")
	}
	if restart {
		parts = append(parts, "restart")
	}
	return strings.Join(parts, ",")
}

// printSettings writes the settings table.
func printSettings(w io.Writer, list []setting) error {
	rows := make([][]string, 0, len(list))
	for _, s := range list {
		rows = append(rows, []string{s.Key, valueText(s.Value), s.Source, valueText(s.Default), note(s.Locked, s.Restart)})
	}
	return output.Table(w, []string{"KEY", "VALUE", "SOURCE", "DEFAULT", "NOTE"}, rows)
}

// printPictures writes the pictures table.
func printPictures(w io.Writer, list []picture) error {
	rows := make([][]string, 0, len(list))
	for _, p := range list {
		n := ""
		if p.Locked {
			n = "locked"
		}
		rows = append(rows, []string{p.Key, p.State, p.Type, n})
	}
	return output.Table(w, []string{"PICTURE", "STATE", "TYPE", "NOTE"}, rows)
}

// pictureLines writes one picture as key=value lines.
func pictureLines(w io.Writer, p picture) error {
	return output.KeyValues(w, []output.KV{
		{Key: "key", Value: p.Key},
		{Key: "state", Value: p.State},
		{Key: "type", Value: p.Type},
		{Key: "sha256", Value: p.SHA256},
		{Key: "locked", Value: strconv.FormatBool(p.Locked)},
	})
}

// fail reports err and returns its exit code. A refusal of the settings routes adds what the person needs to act: the
// keys and the problems the server named, and, for a 403 with a key that is not an installation admin key, which key is
// needed. Under --json the error stays the one envelope line on stderr, so a script can read it.
func fail(stderr io.Writer, asJSON bool, kind string, err error) int {
	code := output.Fail(stderr, asJSON, err)
	var ae *client.APIError
	if asJSON || !errors.As(err, &ae) {
		return code
	}
	switch ae.Reason {
	case "invalid_setting":
		for _, line := range stringList(ae.Details["errors"]) {
			fmt.Fprintln(stderr, "  "+plain(line))
		}
	case "setting_locked", "unknown_setting":
		if keys := stringList(ae.Details["keys"]); len(keys) > 0 {
			fmt.Fprintln(stderr, "keys: "+plain(strings.Join(keys, ", ")))
		}
	}
	if ae.Status == 403 && kind != profile.KindAdmin {
		fmt.Fprintln(stderr, adminHint)
	}
	return code
}

// stringList reads a JSON list of strings, and ignores anything else.
func stringList(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// plain makes server text safe for a terminal: every control or format character becomes a space.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}

// usage is a usage error, exit 2.
func usage(format string, args ...any) error { return exitcode.Usagef(format, args...) }
