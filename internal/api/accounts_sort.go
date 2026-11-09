// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// GET /accounts takes ?sort= and ?order=, as a children listing does (children_sort.go, decision 47). Neither given keeps
// the list in creation order, with the plain cursor it has always had. Given either, the list is sorted: a missing sort is
// created, and a missing order is asc. A sorted cursor is sealed as a children cursor is: AES-GCM under a key derived from
// the links key, with the caller and the sort as its additional data. It opens only for the same walk, and a cursor from
// another walk is refused as one fixed invalid_request.

const accountCursorLabel = "selfcloud accounts cursor v1"

// accountCursorPrefix marks a sorted cursor, so a default listing refuses it and a sorted listing refuses a default one.
const accountCursorPrefix = "a1."

// accountCursorBad is the one answer to a sorted cursor that does not open, whatever the reason.
var accountCursorBad = apierr.New(apierr.InvalidRequest, "cursor is not valid for this sort; start the listing again")

// accountSortParams reads ?sort= and ?order=. sorted is false when neither is given.
func accountSortParams(r *http.Request) (order store.AccountSort, sorted bool, err error) {
	q := r.URL.Query()
	if !q.Has("sort") && !q.Has("order") {
		return order, false, nil
	}
	sortVals, orderVals := q["sort"], q["order"]
	if len(sortVals) > 1 || len(orderVals) > 1 {
		return order, false, apierr.New(apierr.InvalidRequest, "sort and order may each be given once")
	}
	key := "created"
	if len(sortVals) == 1 {
		key = sortVals[0]
	}
	switch key {
	case "name":
		order.ByName = true
	case "created":
	default:
		return order, false, apierr.New(apierr.InvalidRequest, "sort must be name or created")
	}
	dir := "asc"
	if len(orderVals) == 1 {
		dir = orderVals[0]
	}
	switch dir {
	case "asc":
	case "desc":
		order.Desc = true
	default:
		return order, false, apierr.New(apierr.InvalidRequest, "order must be asc or desc")
	}
	return order, true, nil
}

// accountCursorAAD binds a sorted cursor to its caller and its sort.
func accountCursorAAD(p *auth.Principal, order store.AccountSort) []byte {
	tag := fmt.Sprintf("%t/%t", order.ByName, order.Desc)
	return []byte(strings.Join([]string{p.AccountID, string(p.Kind), p.ID, tag}, "\x00"))
}

// accountCursorAEAD returns the cipher that seals sorted account cursors, made from the current links key on each use.
func (s *Server) accountCursorAEAD() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, accountCursorLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealAccountCursor returns the cursor that resumes the sorted account list for p after pos.
func (s *Server) sealAccountCursor(p *auth.Principal, order store.AccountSort, pos store.AccountPos) (string, error) {
	aead, err := s.accountCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// The name is last, so a name with a NUL in it still splits into the same three parts.
	plain := strings.Join([]string{strconv.FormatInt(pos.Created, 10), pos.ID, pos.Name}, "\x00")
	sealed := aead.Seal(nonce, nonce, []byte(plain), accountCursorAAD(p, order))
	return accountCursorPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openAccountCursor returns the position a sorted account cursor resumes after. A cursor that does not open, for any
// reason, is invalid_request.
func (s *Server) openAccountCursor(p *auth.Principal, order store.AccountSort, c string) (store.AccountPos, error) {
	if !strings.HasPrefix(c, accountCursorPrefix) {
		return store.AccountPos{}, accountCursorBad
	}
	aead, err := s.accountCursorAEAD()
	if err != nil {
		return store.AccountPos{}, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(c, accountCursorPrefix))
	if err != nil || len(raw) < aead.NonceSize() {
		return store.AccountPos{}, accountCursorBad
	}
	nonce, sealed := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, accountCursorAAD(p, order))
	if err != nil {
		return store.AccountPos{}, accountCursorBad
	}
	parts := strings.SplitN(string(plain), "\x00", 3)
	if len(parts) != 3 {
		return store.AccountPos{}, accountCursorBad
	}
	created, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return store.AccountPos{}, accountCursorBad
	}
	return store.AccountPos{Created: created, ID: parts[1], Name: parts[2]}, nil
}

// writeSortedAccounts writes one page of the accounts that f narrows to, in the order order. page.Cursor, when set, is a
// sorted cursor.
func (s *Server) writeSortedAccounts(w http.ResponseWriter, r *http.Request, f store.AccountFilter, order store.AccountSort, page store.Page) error {
	p := principalOf(r)
	var after *store.AccountPos
	if page.Cursor != "" {
		pos, err := s.openAccountCursor(p, order, page.Cursor)
		if err != nil {
			return err
		}
		after = &pos
	}
	accounts, more, err := s.store.AccountsSorted(r.Context(), f, order, page.Limit, after)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[accountJSON]{Items: []accountJSON{}}
	for _, a := range accounts {
		out.Items = append(out.Items, toAccountJSON(a))
	}
	if more {
		next, err := s.sealAccountCursor(p, order, order.PosOf(accounts[len(accounts)-1]))
		if err != nil {
			return err
		}
		out.NextCursor = next
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
