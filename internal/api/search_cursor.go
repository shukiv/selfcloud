// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/search"
)

// A search cursor is sealed like a mention reference (decision 40): AES-GCM under a key derived from the links signing key,
// with the account, the caller and the request's fingerprint as its additional data. So a cursor opens only for the
// request, the account and the caller that made it. It shows no rowid, which is shared by every account. A key rotation
// makes every cursor fail to open, and the client starts its search again.
const searchCursorLabel = "selfcloud search cursor v1"

// searchCursorBad is the one answer to a cursor that does not open, whatever the reason.
var searchCursorBad = apierr.New(apierr.InvalidRequest, "cursor is not valid for this search; start the search again")

// searchCursorAEAD returns the cipher that seals search cursors, made from the current links key on each use.
func (s *Server) searchCursorAEAD() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, searchCursorLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// searchCursorAAD binds a cursor to its account, its caller and its request.
func searchCursorAAD(p *auth.Principal, req *search.Request) []byte {
	return []byte(strings.Join([]string{p.AccountID, string(p.Kind), p.ID, req.Fingerprint()}, "\x00"))
}

// searchCursorSize is the plaintext of a cursor: the position of a name search is one row ID (8 bytes); a content search
// reads two, the content row and the node's rowid (16 bytes). The request's fingerprint is additional data, so a cursor of
// one kind does not open for the other.
func searchCursorSize(req *search.Request) int {
	if req.In == search.InContent {
		return 16
	}
	return 8
}

// sealSearchCursor returns the cursor that resumes req for p after the position pos.
func (s *Server) sealSearchCursor(p *auth.Principal, req *search.Request, pos search.Pos) (string, error) {
	aead, err := s.searchCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := make([]byte, searchCursorSize(req))
	binary.BigEndian.PutUint64(plain[:8], uint64(pos.A))
	if len(plain) == 16 {
		binary.BigEndian.PutUint64(plain[8:], uint64(pos.B))
	}
	sealed := aead.Seal(nonce, nonce, plain, searchCursorAAD(p, req))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openSearchCursor returns the position a cursor resumes after. A cursor that does not open, for any reason, is
// invalid_request, so the answer tells nothing about which part was wrong.
func (s *Server) openSearchCursor(p *auth.Principal, req *search.Request, c string) (search.Pos, error) {
	aead, err := s.searchCursorAEAD()
	if err != nil {
		return search.Pos{}, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || len(raw) < aead.NonceSize() {
		return search.Pos{}, searchCursorBad
	}
	nonce, sealed := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, sealed, searchCursorAAD(p, req))
	if err != nil || len(plain) != searchCursorSize(req) {
		return search.Pos{}, searchCursorBad
	}
	pos := search.Pos{A: int64(binary.BigEndian.Uint64(plain[:8]))}
	if len(plain) == 16 {
		pos.B = int64(binary.BigEndian.Uint64(plain[8:]))
	}
	if pos.A < 0 || pos.B < 0 {
		return search.Pos{}, searchCursorBad
	}
	return pos, nil
}
