// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

const (
	maxJSONBody     = 1 << 20
	defaultPageSize = 50
	maxPageSize     = 500
)

// decodeJSON reads a JSON body into v. Unknown fields are refused, so a misspelt field fails
// loudly instead of being ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return apierr.New(apierr.InvalidRequest, "the body must be JSON (Content-Type: application/json)")
	}
	// The body must arrive within jsonBodyTimeout, so a client that stalls mid-body ends here (sec1, decision 84).
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(jsonBodyTimeout))
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return apierr.Newf(apierr.InvalidRequest, "the JSON body is larger than %d bytes", maxJSONBody)
		}
		return apierr.Newf(apierr.InvalidRequest, "invalid JSON body: %v", err)
	}
	if dec.More() {
		return apierr.New(apierr.InvalidRequest, "unexpected data after the JSON body")
	}
	return nil
}

// pageParams reads ?limit= and ?cursor=.
func pageParams(r *http.Request) (store.Page, error) {
	p := store.Page{Limit: defaultPageSize, Cursor: r.URL.Query().Get("cursor")}
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxPageSize {
			return p, apierr.Newf(apierr.InvalidRequest, "limit must be between 1 and %d", maxPageSize)
		}
		p.Limit = n
	}
	return p, nil
}

// listResponse is the shape of every paged list.
type listResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// storeErr maps store errors that callers can cause to API errors.
func storeErr(err error) error {
	if errors.Is(err, store.ErrBadCursor) {
		return apierr.New(apierr.InvalidRequest, "the cursor is not valid")
	}
	if errors.Is(err, store.ErrBadFilter) {
		return filterBad
	}
	return err
}

// cleanLabel trims a human-readable name and checks it: 1 to max characters, no control
// characters.
func cleanLabel(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apierr.Newf(apierr.InvalidRequest, "%s is required", field)
	}
	if utf8.RuneCountInString(s) > max {
		return "", apierr.Newf(apierr.InvalidRequest, "%s is longer than %d characters", field, max)
	}
	if err := checkText(field, s); err != nil {
		return "", err
	}
	return s, nil
}

// cleanExternalID checks a client reference; "" means none. It is kept exactly as given.
func cleanExternalID(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) > 255 {
		return "", apierr.New(apierr.InvalidRequest, "external_id is longer than 255 bytes")
	}
	return s, checkText("external_id", s)
}

// CheckText is the rule for a free text the API stores: valid UTF-8 and no control character. The admin commands apply
// it to what they store too, so a local account is held to the same rule as one made through the API.
func CheckText(field, s string) error { return checkText(field, s) }

func checkText(field, s string) error {
	if !utf8.ValidString(s) {
		return apierr.Newf(apierr.InvalidRequest, "%s is not valid UTF-8", field)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return apierr.Newf(apierr.InvalidRequest, "%s contains a control character", field)
		}
	}
	return nil
}

func notFound(what string) error { return apierr.New(apierr.NotFound, fmt.Sprintf("no such %s", what)) }
