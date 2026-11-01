// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// A listing of children sorted by the server (decision 47) takes ?sort= and ?order=. Neither given keeps the default
// order, which is the name key, and its cursor is the plain one the listing has always had. Given either, the listing is
// sorted: a missing sort is name, and a missing order is asc. The sorted cursor is sealed as a search cursor is (decision
// 40): AES-GCM under a key derived from the links key, with the account, the caller, the directory and the sort as its
// additional data. So it opens only for the same walk: a changed sort or order, or a cursor from the default order, does
// not open, and the answer is one fixed invalid_request.

// childSortKeys are the values ?sort= takes. A sort outside this map is refused; the value never reaches SQL.
var childSortKeys = map[string]store.SortKey{
	"name":     store.SortName,
	"size":     store.SortSize,
	"modified": store.SortModified,
}

const childCursorLabel = "selfcloud children cursor v1"

// childCursorPrefix marks a sorted cursor. A cursor of the default walk is base64 with no dot, so a sorted cursor is never
// read as one, and a default listing refuses a sorted cursor outright. Without the mark, a sealed cursor could split into the
// two parts the default cursor has, depending on the nonce, and open there.
const childCursorPrefix = "c1."

// isSortedCursor says whether c is a sorted cursor (childCursorPrefix).
func isSortedCursor(c string) bool { return strings.HasPrefix(c, childCursorPrefix) }

// childCursorBad is the one answer to a sorted cursor that does not open, whatever the reason.
var childCursorBad = apierr.New(apierr.InvalidRequest, "cursor is not valid for this sort; start the listing again")

// childSortParams reads ?sort= and ?order=. sorted is false when neither is given.
func childSortParams(r *http.Request) (order store.ChildSort, sorted bool, err error) {
	q := r.URL.Query()
	if !q.Has("sort") && !q.Has("order") {
		return store.ChildSort{Key: store.SortName}, false, nil
	}
	sortVals, orderVals := q["sort"], q["order"]
	if len(sortVals) > 1 || len(orderVals) > 1 {
		return order, false, apierr.New(apierr.InvalidRequest, "sort and order may each be given once")
	}
	key := "name"
	if len(sortVals) == 1 {
		key = sortVals[0]
	}
	k, ok := childSortKeys[key]
	if !ok {
		return order, false, apierr.New(apierr.InvalidRequest, "sort must be name, size or modified")
	}
	dir := "asc"
	if len(orderVals) == 1 {
		dir = orderVals[0]
	}
	if dir != "asc" && dir != "desc" {
		return order, false, apierr.New(apierr.InvalidRequest, "order must be asc or desc")
	}
	return store.ChildSort{Key: k, Desc: dir == "desc"}, true, nil
}

// childSortTag names the sort in the additional data of a cursor.
func childSortTag(order store.ChildSort) string {
	return strconv.Itoa(int(order.Key)) + "/" + strconv.FormatBool(order.Desc)
}

// childCursorAAD binds a sorted cursor to its account, its caller, its directory and its sort.
func childCursorAAD(p *auth.Principal, dirID string, order store.ChildSort) []byte {
	return []byte(strings.Join([]string{p.AccountID, string(p.Kind), p.ID, dirID, childSortTag(order)}, "\x00"))
}

// childCursorAEAD returns the cipher that seals sorted cursors, made from the current links key on each use.
func (s *Server) childCursorAEAD() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, childCursorLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealChildCursor returns the cursor that resumes the sorted walk of dirID for p after pos.
func (s *Server) sealChildCursor(p *auth.Principal, dirID string, order store.ChildSort, pos store.ChildPos) (string, error) {
	aead, err := s.childCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := strings.Join([]string{pos.Kind, pos.Text, strconv.FormatInt(pos.Num, 10), pos.ID}, "\x00")
	sealed := aead.Seal(nonce, nonce, []byte(plain), childCursorAAD(p, dirID, order))
	return childCursorPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openChildCursor returns the position a sorted cursor resumes after. A cursor that does not open, for any reason,
// is invalid_request.
func (s *Server) openChildCursor(p *auth.Principal, dirID string, order store.ChildSort, c string) (store.ChildPos, error) {
	aead, err := s.childCursorAEAD()
	if err != nil {
		return store.ChildPos{}, err
	}
	if !isSortedCursor(c) {
		return store.ChildPos{}, childCursorBad
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(c, childCursorPrefix))
	if err != nil || len(raw) < aead.NonceSize() {
		return store.ChildPos{}, childCursorBad
	}
	nonce, sealed := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, childCursorAAD(p, dirID, order))
	if err != nil {
		return store.ChildPos{}, childCursorBad
	}
	parts := strings.Split(string(plain), "\x00")
	if len(parts) != 4 {
		return store.ChildPos{}, childCursorBad
	}
	num, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return store.ChildPos{}, childCursorBad
	}
	return store.ChildPos{Kind: parts[0], Text: parts[1], Num: num, ID: parts[3]}, nil
}

// sortedChildren reads one page of dir's children in the order order. page.Cursor, when set, is a sorted cursor.
func (s *Server) sortedChildren(r *http.Request, dir *store.Node, order store.ChildSort, page store.Page) ([]*store.Node, string, error) {
	ctx := r.Context()
	p := principalOf(r)
	var after *store.ChildPos
	if page.Cursor != "" {
		pos, err := s.openChildCursor(p, dir.ID, order, page.Cursor)
		if err != nil {
			return nil, "", err
		}
		after = &pos
	}
	kids, more, err := s.store.ChildrenSorted(ctx, dir.AccountID, dir.ID, order, page.Limit, after)
	if err != nil {
		return nil, "", storeErr(err)
	}
	if !more {
		return kids, "", nil
	}
	next, err := s.sealChildCursor(p, dir.ID, order, order.PosOf(kids[len(kids)-1]))
	if err != nil {
		return nil, "", err
	}
	return kids, next, nil
}
