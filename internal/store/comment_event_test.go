// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "testing"

// TestCommentEventNamesTheActorOnlyWhenNotTheAuthor: the author's own change carries no actor_id, another person's change
// names that person, and a comment whose author was deleted has a null author_id.
func TestCommentEventNamesTheActorOnlyWhenNotTheAuthor(t *testing.T) {
	t.Parallel()
	c := &Comment{ID: "cmt_1", NodeID: "nod_1", AuthorID: "usr_author"}

	own := CommentEvent(c, "fld_1", "usr_author", 0)
	if own.ActorID != nil || own.AuthorID == nil || *own.AuthorID != "usr_author" {
		t.Fatalf("own change: actor_id %v author_id %v, want no actor_id and author_id usr_author", own.ActorID, own.AuthorID)
	}
	other := CommentEvent(c, "fld_1", "usr_manager", 0)
	if other.ActorID == nil || *other.ActorID != "usr_manager" {
		t.Fatalf("other change: actor_id %v, want usr_manager", other.ActorID)
	}

	gone := &Comment{ID: "cmt_2", NodeID: "nod_1"}
	g := CommentEvent(gone, "fld_1", "usr_manager", 0)
	if g.AuthorID != nil || g.ActorID == nil {
		t.Fatalf("author deleted: author_id %v actor_id %v, want null author_id and an actor_id", g.AuthorID, g.ActorID)
	}
}

// TestCommentEventReplyAndMentions: a reply names its parent, a top-level comment has no reply_to_id, and mentions is the
// count it is given.
func TestCommentEventReplyAndMentions(t *testing.T) {
	t.Parallel()
	reply := &Comment{ID: "cmt_r", NodeID: "nod_1", ParentID: "cmt_p", AuthorID: "usr_a"}
	r := CommentEvent(reply, "fld_1", "usr_a", 3)
	if r.ReplyToID == nil || *r.ReplyToID != "cmt_p" || r.Mentions != 3 || r.FolderID != "fld_1" || r.NodeID != "nod_1" || r.ID != "cmt_r" {
		t.Fatalf("reply = %+v, want id cmt_r, reply_to_id cmt_p, mentions 3, folder_id fld_1, node_id nod_1", r)
	}
	top := CommentEvent(&Comment{ID: "cmt_t", NodeID: "nod_1", AuthorID: "usr_a"}, "fld_1", "usr_a", 0)
	if top.ReplyToID != nil {
		t.Fatalf("top-level comment reply_to_id = %v, want nil", *top.ReplyToID)
	}
}
