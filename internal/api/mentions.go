// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Mentions (design section 14, slice r, decision 39). A person is named by a reference, not by a user ID: a reference is
// the user's ID sealed under a key derived from the links signing key, with the account and the node as its additional
// data. So a person reads no ID of another person, the people list can answer with references, and a reference made for
// one node opens for no other. A reference that does not open, or opens to a user who is not active or does not reach the
// node, names nobody, and the answer to the author is the same in every case (decision 39, the enumeration rule).
const (
	// maxPeopleSuggestions is how many people the people list answers with at most.
	maxPeopleSuggestions = 5
	// peopleScanLimit is how many matching users the people list checks for reach at most. A name shared by more users
	// than this may show fewer suggestions.
	peopleScanLimit = 50
	// peopleSearchRate is the limiter of the people list, per user.
	peopleSearchRate = "people.search"
	// mentionRefPrefix starts every reference, so a reference is told apart from an ID.
	mentionRefPrefix = "mnt_"
	// mentionRefLabel labels the key derived for references, so it is not the key of another use.
	mentionRefLabel = "selfcloud comment mention reference v1"
)

// personJSON is one suggestion: a reference to name the person by, and the person's display name.
type personJSON struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

// mentionAEAD returns the cipher that seals mention references. It is made from the links signing key on each use, which
// is cheap, so no key is kept beside the server.
func (s *Server) mentionAEAD() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, s.links.current, nil, mentionRefLabel, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// mentionAAD binds a reference to its account and node.
func mentionAAD(accountID, nodeID string) []byte {
	return []byte(accountID + "\x00" + nodeID)
}

// sealMention returns the reference of userID for a node of an account.
func (s *Server) sealMention(accountID, nodeID, userID string) (string, error) {
	aead, err := s.mentionAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(userID), mentionAAD(accountID, nodeID))
	return mentionRefPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openMention returns the user a reference names for a node of an account. It reports false for a reference that does
// not open for that node and account, which includes every reference made for another one.
func (s *Server) openMention(accountID, nodeID, ref string) (string, bool) {
	body, ok := strings.CutPrefix(ref, mentionRefPrefix)
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	aead, err := s.mentionAEAD()
	if err != nil || len(raw) < aead.NonceSize() {
		return "", false
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], mentionAAD(accountID, nodeID))
	if err != nil || !id.Valid("usr", string(plain)) {
		return "", false
	}
	return string(plain), true
}

// openMentions turns the references of a comment into the distinct users they name, for the node. Two references to one
// person name that person once. A reference that does not open is refused: a client that forged one, or copied one from
// another node, gets invalid_request. That answer does not depend on any user. More than maxCommentMentions people is
// refused too, before any reach is looked at.
func (s *Server) openMentions(accountID, nodeID string, refs []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		u, ok := s.openMention(accountID, nodeID, ref)
		if !ok {
			return nil, apierr.New(apierr.InvalidRequest, "a person named in the comment is not valid for this item")
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	if len(out) > maxCommentMentions {
		return nil, apierr.Newf(apierr.InvalidRequest, "a comment may name at most %d people", maxCommentMentions).
			With("reason", "mentions")
	}
	return out, nil
}

// reachesNode reports whether user userID reaches node nodeID at any role, through the same walk every read uses
// (nodeRoleIn, with the organisation links). A user who is missing, not active, or outside the account does not reach it:
// those cases answer the same as a user with no permission on the node. Any other error is returned.
func reachesNode(ctx context.Context, rd accessReader, accountID, nodeID, userID string) (bool, error) {
	p := &auth.Principal{Kind: auth.KindUser, ID: userID, UserID: userID, AccountID: accountID}
	_, role, err := nodeRoleIn(ctx, rd, p, nodeID, false, true)
	if err != nil {
		switch apierr.From(err).Code {
		case apierr.NotFound, apierr.Unauthenticated, apierr.UserDisabled:
			return false, nil
		}
		return false, err
	}
	return store.RoleRank(role) >= store.RoleRank(store.RoleViewer), nil
}

// resolveMentions returns the users of userIDs who may be named in a comment on the node: each is active, reaches the node
// inside the write, and is not the author. Every other ID is dropped without an error. The write transaction is the one
// that stores the comment, so the people named are those who reach the node when it is written.
func resolveMentions(ctx context.Context, tx *store.Tx, accountID, nodeID, authorID string, userIDs []string) ([]string, error) {
	var out []string
	for _, u := range userIDs {
		if u == authorID {
			continue
		}
		ok, err := reachesNode(ctx, tx, accountID, nodeID, u)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// handleListPeople suggests people to name in a comment on a node: the active users of the account whose name contains
// the text, who reach the node, at most five. Only a signed-in user may ask, not a token and not an account key. A caller
// who does not reach the node gets not_found, and a viewer, who reaches it but may not comment, gets forbidden.
func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownCommentCaller(p, "search for people to name"); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if n := utf8.RuneCountInString(q); n < directoryMinQuery || n > directoryMaxQuery {
		return apierr.Newf(apierr.InvalidRequest, "q must be between %d and %d characters", directoryMinQuery, directoryMaxQuery)
	}
	if err := s.rateLimit(peopleSearchRate, p.UserID); err != nil {
		return err
	}
	ctx := r.Context()
	if err := commentsOpen(ctx, s.store, p.AccountID); err != nil {
		return err
	}
	_, role, err := commentNode(ctx, s.store, p, nodeID)
	if err != nil {
		return err
	}
	if store.RoleRank(role) < store.RoleRank(store.RoleCommenter) {
		return apierr.New(apierr.Forbidden, "this needs the "+store.RoleCommenter+" role on the item").With("reason", "role")
	}
	candidates, err := s.store.ActiveUsersNamed(ctx, p.AccountID, q, peopleScanLimit)
	if err != nil {
		return err
	}
	items := []personJSON{}
	for _, c := range candidates {
		if len(items) == maxPeopleSuggestions {
			break
		}
		ok, err := reachesNode(ctx, s.store, p.AccountID, nodeID, c.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ref, err := s.sealMention(p.AccountID, nodeID, c.ID)
		if err != nil {
			return err
		}
		items = append(items, personJSON{Ref: ref, Name: c.Name})
	}
	writeJSON(w, http.StatusOK, listResponse[personJSON]{Items: items})
	return nil
}
