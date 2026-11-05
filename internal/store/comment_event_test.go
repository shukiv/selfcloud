// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "testing"

// TestCommentEventNamesTheActorOnlyWhenNotTheAuthor: the author's own change carries no actor, another person's change
// names that person, and a comment whose author was deleted has a null author.
func TestCommentEventNamesTheActorOnlyWhenNotTheAuthor(t *testing.T) {
	t.Parallel()
	c := &Comment{ID: "cmt_1", NodeID: "nod_1", AuthorID: "usr_author"}

	own := CommentEvent(c, "fld_1", "usr_author", 0)
	if own.Actor != nil || own.Author == nil || *own.Author != "usr_author" {
		t.Fatalf("own change: actor %v author %v, want no actor and author usr_author", own.Actor, own.Author)
	}
	other := CommentEvent(c, "fld_1", "usr_manager", 0)
	if other.Actor == nil || *other.Actor != "usr_manager" {
		t.Fatalf("other change: actor %v, want usr_manager", other.Actor)
	}

	gone := &Comment{ID: "cmt_2", NodeID: "nod_1"}
	g := CommentEvent(gone, "fld_1", "usr_manager", 0)
	if g.Author != nil || g.Actor == nil {
		t.Fatalf("author deleted: author %v actor %v, want null author and an actor", g.Author, g.Actor)
	}
}

// TestCommentEventReplyAndMentions: a reply names its parent, a top-level comment has no reply_to, and mentions is the
// count it is given.
func TestCommentEventReplyAndMentions(t *testing.T) {
	t.Parallel()
	reply := &Comment{ID: "cmt_r", NodeID: "nod_1", ParentID: "cmt_p", AuthorID: "usr_a"}
	r := CommentEvent(reply, "fld_1", "usr_a", 3)
	if r.ReplyTo == nil || *r.ReplyTo != "cmt_p" || r.Mentions != 3 || r.Folder != "fld_1" || r.Node != "nod_1" {
		t.Fatalf("reply = %+v, want reply_to cmt_p, mentions 3, folder fld_1, node nod_1", r)
	}
	top := CommentEvent(&Comment{ID: "cmt_t", NodeID: "nod_1", AuthorID: "usr_a"}, "fld_1", "usr_a", 0)
	if top.ReplyTo != nil {
		t.Fatalf("top-level comment reply_to = %v, want nil", *top.ReplyTo)
	}
}
