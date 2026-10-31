// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// Comments (design section 14, slice r, decision 39): a comment on a file or a folder, and one level of replies. Reading
// needs viewer and up, writing commenter and up, and both are checked by the node walk (nodeAccessWith, and
// requireNodeRole inside the write transaction). A comment is plain text: the API stores it and returns it as text, and
// no client may render it as HTML.
const (
	// maxCommentChars is the longest body, in characters after NFC normalisation.
	maxCommentChars = 2000
	// maxCommentLines is the most lines a body may have. A line ends at a line feed; a carriage return is made one.
	maxCommentLines = 50
	// maxCommentMentions is how many people one comment may name.
	maxCommentMentions = 10
	// maxCommentsPerNode is how many live comments one node may hold. A write past it is refused with conflict.
	maxCommentsPerNode = 5000
	// commentRepliesShown is how many replies a top-level comment carries in a list. The rest are read with ?parent=.
	commentRepliesShown = 50
	// commentWriteRate is the limiter of comment writes, per user.
	commentWriteRate = "comment.write"
)

// commentJSON is one comment, as a reader sees it. A tombstone (a deleted comment that has replies) shows deleted and
// nothing else. Names are display names; a comment carries no user ID. Mentions are names too, and a mention of a person
// who no longer reaches the node is not in the stored list at all (decision 39).
type commentJSON struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
	Body     string `json:"body,omitempty"`
	// Mine is set on the caller's own comment.
	Mine bool `json:"mine,omitempty"`
	// AuthorName is the author's display name. AuthorGone is set when the author's user was deleted: the interface then
	// shows "former member", translated, and no name.
	AuthorName string           `json:"author_name,omitempty"`
	AuthorGone bool             `json:"author_gone,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	EditedAt   *time.Time       `json:"edited_at,omitempty"`
	Mentions   []commentMention `json:"mentions"`
	// Resolved, ResolvedAt and the resolver's name are set on a resolved top-level comment. ResolvedByGone is set when the
	// resolver was deleted.
	Resolved       bool       `json:"resolved"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolvedByName string     `json:"resolved_by_name,omitempty"`
	ResolvedByGone bool       `json:"resolved_by_gone,omitempty"`
	// ReplyCount is how many replies the comment has in all. Replies carries the first of them, and RepliesMore is set
	// when there are more to read with ?parent=.
	ReplyCount  int           `json:"reply_count,omitempty"`
	Replies     []commentJSON `json:"replies,omitempty"`
	RepliesMore bool          `json:"replies_more,omitempty"`
}

// commentMention is one person a comment names, by display name.
type commentMention struct {
	Name string `json:"name"`
}

// commentsOff is the refusal of every comment route while the account's switch is off.
func commentsOff() error {
	return apierr.New(apierr.Forbidden, "comments are turned off for this account").With("reason", "comments_off")
}

// commentsOpen refuses when the account's comment switch is off. It is the one rule, read through rd: the store for the
// reads, which run before the node is looked at so they reveal nothing about a node, and a write transaction for the
// writes, which see the switch as it is then.
func commentsOpen(ctx context.Context, rd interface {
	CommentsOn(ctx context.Context, accountID string) (bool, error)
}, accountID string) error {
	on, err := rd.CommentsOn(ctx, accountID)
	if err != nil {
		return err
	}
	if !on {
		return commentsOff()
	}
	return nil
}

// ownCommentCaller is the caller of the calls that change one comment or read the people it may name: a signed-in user,
// not a delegated token and not an account key. The author of a comment is a person, so only the person acts for it.
func ownCommentCaller(p *auth.Principal, what string) error {
	if err := userCaller(p, what); err != nil {
		return err
	}
	if p.Token != nil {
		return apierr.New(apierr.Forbidden, "a delegated token cannot "+what+"; only the signed-in user can")
	}
	return nil
}

// cleanCommentBody checks a comment body and returns its NFC form. It is refused rather than trimmed, so the text the
// reader sees is the text the author typed. A carriage return or a CRLF is made a line feed, so a body has one kind of
// line break. The body is valid UTF-8, not blank, at most maxCommentChars characters and maxCommentLines lines, and holds
// no control character but the line feed, no bidi control and no U+061C (checkTextChars). A blank body is one with nothing
// to show: only white space and characters with no width. A body is stored as text and never rendered as HTML.
func cleanCommentBody(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", apierr.New(apierr.InvalidRequest, "the comment must be valid UTF-8")
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = norm.NFC.String(s)
	if blankText(s) {
		return "", apierr.New(apierr.InvalidRequest, "the comment is required")
	}
	if utf8.RuneCountInString(s) > maxCommentChars {
		return "", apierr.Newf(apierr.InvalidRequest, "a comment may be at most %d characters", maxCommentChars)
	}
	if strings.Count(s, "\n")+1 > maxCommentLines {
		return "", apierr.Newf(apierr.InvalidRequest, "a comment may have at most %d lines", maxCommentLines)
	}
	if err := checkTextChars(s, true); err != nil {
		return "", err
	}
	return s, nil
}

// blankText reports whether s shows nothing: every character is a white space or has no width. A blank body is refused
// like an empty one.
func blankText(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && !noWidth(r) {
			return false
		}
	}
	return true
}

// noWidth reports whether r is a character that shows no width: a format character (a zero-width space or joiner, a byte
// order mark, a bidi mark), or one of the fillers that look blank (Braille blank, the Hangul fillers).
func noWidth(r rune) bool {
	switch r {
	case 0x2800, 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// commentViews turns comments into their JSON for the caller p. Names are read in one query for the whole list, and only
// for a caller that may learn names (peopleNamesSeen). Mentions are read in one query too.
func (s *Server) commentViews(ctx context.Context, p *auth.Principal, accountID string, cs []*store.Comment) ([]commentJSON, error) {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	mentions, err := s.store.MentionedUsers(ctx, accountID, ids)
	if err != nil {
		return nil, err
	}
	var people []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			people = append(people, u)
		}
	}
	for _, c := range cs {
		if c.DeletedAt != nil {
			continue
		}
		add(c.AuthorID)
		add(c.ResolvedBy)
		for _, u := range mentions[c.ID] {
			add(u)
		}
	}
	names := map[string]string{}
	if peopleNamesSeen(p, accountID) {
		if names, err = s.store.UserNames(ctx, accountID, people); err != nil {
			return nil, err
		}
	}
	out := make([]commentJSON, len(cs))
	for i, c := range cs {
		out[i] = commentView(p, c, mentions[c.ID], names, peopleNamesSeen(p, accountID))
	}
	return out, nil
}

// commentView is one comment's JSON. A tombstone shows nothing but that it was deleted.
func commentView(p *auth.Principal, c *store.Comment, mentioned []string, names map[string]string, showNames bool) commentJSON {
	v := commentJSON{ID: c.ID, ParentID: c.ParentID, CreatedAt: c.CreatedAt, EditedAt: c.EditedAt, Mentions: []commentMention{}}
	if c.DeletedAt != nil {
		v.Deleted = true
		return v
	}
	v.Body = c.Body
	v.Mine = p.UserID != "" && c.AuthorID == p.UserID
	if c.AuthorID == "" {
		v.AuthorGone = true
	} else if showNames {
		v.AuthorName = names[c.AuthorID]
	}
	if showNames {
		for _, u := range mentioned {
			v.Mentions = append(v.Mentions, commentMention{Name: names[u]})
		}
	}
	if c.ResolvedAt != nil {
		v.Resolved = true
		v.ResolvedAt = c.ResolvedAt
		if c.ResolvedBy == "" {
			v.ResolvedByGone = true
		} else if showNames {
			v.ResolvedByName = names[c.ResolvedBy]
		}
	}
	return v
}

// requireCommenter is requireNodeRole for a comment write: the same rule, with the refusal naming the role, so a viewer's
// refusal says reason role (decision 39). A node the caller does not reach is still not_found.
func requireCommenter(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string) (*store.Node, error) {
	n, err := requireNodeRole(ctx, rd, p, nodeID, store.RoleCommenter)
	if err != nil && apierr.From(err).Code == apierr.Forbidden {
		// The message is roleTooLow's, with the reason added.
		return nil, apierr.New(apierr.Forbidden, "this needs the "+store.RoleCommenter+" role on the item").With("reason", "role")
	}
	return n, err
}

// commentNode reads a node for a comment route: it must be one the caller reaches at viewer or above, and it must be a
// file or a folder, never a shortcut (a shortcut has no comments of its own; decision 39).
func commentNode(ctx context.Context, rd accessReader, p *auth.Principal, nodeID string) (*store.Node, string, error) {
	n, role, err := nodeAccessWith(ctx, rd, p, nodeID)
	if err != nil {
		return nil, "", err
	}
	if n.Kind == store.KindShortcut {
		return nil, "", nodeNotFound()
	}
	return n, role, nil
}

// handleListComments lists the top-level comments of a node, newest first, with the first replies of each. With
// ?parent=cmt_... it lists the replies of that comment, oldest first. A viewer reads them; so does a token of any scope
// that reaches the node, and an account key.
func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := callerCheck(p); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	parentID := r.URL.Query().Get("parent")
	if parentID != "" && !id.Valid("cmt", parentID) {
		return notFound("comment")
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := commentsOpen(ctx, s.store, p.AccountID); err != nil {
		return err
	}
	if _, _, err := commentNode(ctx, s.store, p, nodeID); err != nil {
		return err
	}
	if parentID != "" {
		return s.writeReplies(w, r, p, nodeID, parentID, page)
	}
	// One more than the page is read, so the answer knows whether a next page exists.
	more := store.Page{Limit: page.Limit + 1, Cursor: page.Cursor}
	cs, err := s.store.TopLevelComments(ctx, p.AccountID, nodeID, more)
	if err != nil {
		return storeErr(err)
	}
	var next string
	if len(cs) > page.Limit {
		cs = cs[:page.Limit]
		next = store.CommentCursor(cs[len(cs)-1])
	}
	parents := make([]string, len(cs))
	for i, c := range cs {
		parents[i] = c.ID
	}
	previews, err := s.store.ReplyPreviews(ctx, p.AccountID, nodeID, parents, commentRepliesShown)
	if err != nil {
		return err
	}
	var all []*store.Comment
	all = append(all, cs...)
	for _, pv := range previews {
		all = append(all, pv.Replies...)
	}
	views, err := s.commentViews(ctx, p, p.AccountID, all)
	if err != nil {
		return err
	}
	byID := make(map[string]commentJSON, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	items := make([]commentJSON, 0, len(cs))
	for _, c := range cs {
		v := byID[c.ID]
		if pv := previews[c.ID]; pv != nil {
			v.ReplyCount = pv.Total
			v.RepliesMore = pv.Total > len(pv.Replies)
			for _, rc := range pv.Replies {
				v.Replies = append(v.Replies, byID[rc.ID])
			}
		}
		items = append(items, v)
	}
	writeJSON(w, http.StatusOK, listResponse[commentJSON]{Items: items, NextCursor: next})
	return nil
}

// writeReplies answers the replies of one top-level comment, a page at a time. The parent must be a top-level comment of
// the node: any other ID is not_found.
func (s *Server) writeReplies(w http.ResponseWriter, r *http.Request, p *auth.Principal, nodeID, parentID string, page store.Page) error {
	ctx := r.Context()
	parent, err := s.store.CommentIn(ctx, p.AccountID, parentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (parent.NodeID != nodeID || parent.ParentID != "")) {
		return notFound("comment")
	}
	if err != nil {
		return err
	}
	cs, err := s.store.ThreadReplies(ctx, p.AccountID, nodeID, parentID, store.Page{Limit: page.Limit + 1, Cursor: page.Cursor})
	if err != nil {
		return storeErr(err)
	}
	var next string
	if len(cs) > page.Limit {
		cs = cs[:page.Limit]
		next = store.CommentCursor(cs[len(cs)-1])
	}
	items, err := s.commentViews(ctx, p, p.AccountID, cs)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, listResponse[commentJSON]{Items: items, NextCursor: next})
	return nil
}

// commentBody is what a write of a comment carries: the text, an optional parent, and the people it names, by the
// references the people list gave (mentions.go).
type commentBody struct {
	Body     string   `json:"body"`
	ParentID string   `json:"parent_id"`
	Mentions []string `json:"mentions"`
}

// handleCreateComment adds a comment, or a reply to a top-level comment. The caller is a signed-in user, or a delegated
// token with write that acts as its user; an account key and a read-only token are refused. The role is checked again
// inside the write, so a permission revoked during the request stops the write. Mentions resolve inside the same write,
// to the people who reach the node then; the rest are dropped without an error, so the answer does not depend on whether
// a named person exists.
func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := changeCaller(p, "comments"); err != nil {
		return err
	}
	nodeID := r.PathValue("nod")
	if !id.Valid("nod", nodeID) {
		return nodeNotFound()
	}
	var in commentBody
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	body, err := cleanCommentBody(in.Body)
	if err != nil {
		return err
	}
	if in.ParentID != "" && !id.Valid("cmt", in.ParentID) {
		return apierr.New(apierr.InvalidRequest, "parent_id is not a comment ID")
	}
	ctx := r.Context()
	if err := s.rateLimit(commentWriteRate, p.UserID); err != nil {
		return err
	}
	userIDs, err := s.openMentions(p.AccountID, nodeID, in.Mentions)
	if err != nil {
		return err
	}
	s.atWrite()
	var created *store.Comment
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := commentsOpen(ctx, tx, p.AccountID); err != nil {
			return err
		}
		n, err := requireCommenter(ctx, tx, p, nodeID)
		if err != nil {
			return err
		}
		if n.Kind == store.KindShortcut {
			return nodeNotFound()
		}
		if in.ParentID != "" {
			if err := checkReplyParent(ctx, tx, p.AccountID, nodeID, in.ParentID); err != nil {
				return err
			}
		}
		live, err := tx.CountLiveComments(ctx, p.AccountID, nodeID, maxCommentsPerNode)
		if err != nil {
			return err
		}
		if live >= maxCommentsPerNode {
			return apierr.Newf(apierr.Conflict, "an item may hold at most %d comments", maxCommentsPerNode).
				With("reason", "limit").With("limit", maxCommentsPerNode)
		}
		resolved, err := resolveMentions(ctx, tx, p.AccountID, nodeID, p.UserID, userIDs)
		if err != nil {
			return err
		}
		c := &store.Comment{ID: id.New("cmt"), AccountID: p.AccountID, NodeID: nodeID, ParentID: in.ParentID,
			AuthorID: p.UserID, Body: body, CreatedAt: store.Now()}
		if err := tx.InsertComment(ctx, c); err != nil {
			return err
		}
		if err := tx.InsertMentions(ctx, p.AccountID, c.ID, resolved); err != nil {
			return err
		}
		// r3 writes the notifications of this comment here, in this transaction: one for each resolved mention, and one
		// for the author of the parent when this is a reply (proposal section 6.1, steps 8 and 9). Nothing is sent yet.
		created = c
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "comment.create", nodeID,
			map[string]any{"comment": c.ID, "reply": in.ParentID != "", "mentions": len(userIDs)}, n.FolderID))
	})
	if err != nil {
		return err
	}
	views, err := s.commentViews(ctx, p, p.AccountID, []*store.Comment{created})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, views[0])
	return nil
}

// checkReplyParent refuses a reply whose parent is not a live top-level comment of the same node. A reply to a reply is
// invalid_request, since the thread holds one level; a parent of another node, a missing one and a tombstone are not_found.
func checkReplyParent(ctx context.Context, tx *store.Tx, accountID, nodeID, parentID string) error {
	parent, err := tx.CommentIn(ctx, accountID, parentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (parent.NodeID != nodeID || parent.DeletedAt != nil)) {
		return notFound("comment")
	}
	if err != nil {
		return err
	}
	if parent.ParentID != "" {
		return apierr.New(apierr.InvalidRequest, "a reply goes on a top-level comment, not on another reply").
			With("reason", "reply_to_reply")
	}
	return nil
}

// handleEditComment changes the body of the caller's own comment. Only the author edits, and only while the author still
// reaches the node as a commenter. An edit does not notify anyone new (decision 39).
func (s *Server) handleEditComment(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownCommentCaller(p, "edit a comment"); err != nil {
		return err
	}
	cid := r.PathValue("cmt")
	if !id.Valid("cmt", cid) {
		return notFound("comment")
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	body, err := cleanCommentBody(in.Body)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.rateLimit(commentWriteRate, p.UserID); err != nil {
		return err
	}
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := commentsOpen(ctx, tx, p.AccountID); err != nil {
			return err
		}
		c, err := ownComment(ctx, tx, p.AccountID, cid)
		if err != nil {
			return err
		}
		n, err := requireCommenter(ctx, tx, p, c.NodeID)
		if err != nil {
			return err
		}
		if c.AuthorID != p.UserID {
			return apierr.New(apierr.Forbidden, "only the author may edit a comment").With("reason", "author")
		}
		if err := tx.UpdateCommentBody(ctx, p.AccountID, cid, body, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "comment.edit", c.NodeID,
			map[string]any{"comment": cid}, n.FolderID))
	})
	if err != nil {
		return err
	}
	return s.writeComment(w, r, p, cid)
}

// ownComment reads a live comment of the account, or not_found. A tombstone is not_found too: it has no body to change
// or resolve.
func ownComment(ctx context.Context, tx *store.Tx, accountID, cid string) (*store.Comment, error) {
	c, err := tx.CommentIn(ctx, accountID, cid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("comment")
	}
	if err != nil {
		return nil, err
	}
	if c.DeletedAt != nil {
		return nil, notFound("comment")
	}
	return c, nil
}

// writeComment answers one comment, read again after its write.
func (s *Server) writeComment(w http.ResponseWriter, r *http.Request, p *auth.Principal, cid string) error {
	c, err := s.store.CommentIn(r.Context(), p.AccountID, cid)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("comment")
	}
	if err != nil {
		return err
	}
	views, err := s.commentViews(r.Context(), p, p.AccountID, []*store.Comment{c})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, views[0])
	return nil
}

// handleDeleteComment deletes a comment. The author may, and so may a manager of the node. A comment with replies
// becomes a tombstone, so its replies keep their thread; any other comment is removed.
func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownCommentCaller(p, "delete a comment"); err != nil {
		return err
	}
	cid := r.PathValue("cmt")
	if !id.Valid("cmt", cid) {
		return notFound("comment")
	}
	ctx := r.Context()
	if err := s.rateLimit(commentWriteRate, p.UserID); err != nil {
		return err
	}
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := commentsOpen(ctx, tx, p.AccountID); err != nil {
			return err
		}
		c, err := ownComment(ctx, tx, p.AccountID, cid)
		if err != nil {
			return err
		}
		n, role, err := nodeAccessWith(ctx, tx, p, c.NodeID)
		if err != nil {
			return err
		}
		if c.AuthorID != p.UserID && store.RoleRank(role) < store.RoleRank(store.RoleManager) {
			return apierr.New(apierr.Forbidden, "only the author or a manager may delete a comment").With("reason", "role")
		}
		tombstone := false
		if c.ParentID == "" {
			if tombstone, err = tx.HasReplies(ctx, p.AccountID, c.NodeID, cid); err != nil {
				return err
			}
		}
		if tombstone {
			err = tx.TombstoneComment(ctx, p.AccountID, cid, store.Now())
		} else {
			err = tx.RemoveComment(ctx, c)
		}
		if err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "comment.delete", c.NodeID,
			map[string]any{"comment": cid, "tombstone": tombstone}, n.FolderID))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleResolveComment marks a top-level comment resolved, or open again. Any commenter may resolve it.
func (s *Server) handleResolveComment(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	if err := ownCommentCaller(p, "resolve a comment"); err != nil {
		return err
	}
	cid := r.PathValue("cmt")
	if !id.Valid("cmt", cid) {
		return notFound("comment")
	}
	var in struct {
		Resolved *bool `json:"resolved"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.Resolved == nil {
		return apierr.New(apierr.InvalidRequest, "resolved must be true or false")
	}
	ctx := r.Context()
	if err := s.rateLimit(commentWriteRate, p.UserID); err != nil {
		return err
	}
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := commentsOpen(ctx, tx, p.AccountID); err != nil {
			return err
		}
		c, err := ownComment(ctx, tx, p.AccountID, cid)
		if err != nil {
			return err
		}
		n, err := requireCommenter(ctx, tx, p, c.NodeID)
		if err != nil {
			return err
		}
		if c.ParentID != "" {
			return apierr.New(apierr.InvalidRequest, "only a top-level comment is resolved").With("reason", "reply")
		}
		by := ""
		if *in.Resolved {
			by = p.UserID
		}
		if err := tx.SetResolved(ctx, p.AccountID, cid, by, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, p.AccountID, "comment.resolve", c.NodeID,
			map[string]any{"comment": cid, "resolved": *in.Resolved}, n.FolderID))
	})
	if err != nil {
		return err
	}
	return s.writeComment(w, r, p, cid)
}
