// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCommentEventsAreListed: the five comment events are in Events, and the list stays sorted.
func TestCommentEventsAreListed(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"comment.created", "comment.updated", "comment.deleted", "comment.resolved", "comment.reopened"} {
		if !ValidEvent(name) {
			t.Fatalf("%s is not a valid event", name)
		}
	}
	if !slices.IsSorted(Events) {
		t.Fatalf("Events is not sorted: %v", Events)
	}
}

// TestCommentPayloadKeys: a comment payload holds exactly the ID keys, and actor_id and tombstone only where they belong.
// The envelope and the data are read back from the bytes a receiver would get.
func TestCommentPayloadKeys(t *testing.T) {
	t.Parallel()
	parent, author, actor := "cmt_parent", "usr_author", "usr_actor"
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	plain := Comment{ID: "cmt_1", ReplyToID: &parent, NodeID: "nod_1", FolderID: "fld_1", AuthorID: &author, Mentions: 2}
	body, err := Body("evt_1", "comment.created", at, "acc_1", nil, plain)
	if err != nil {
		t.Fatal(err)
	}
	got := dataKeys(t, body)
	if strings.Join(got, ",") != "author_id,folder_id,id,mentions,node_id,reply_to_id" {
		t.Fatalf("comment.created keys = %v", got)
	}

	withActor := Comment{ID: "cmt_1", NodeID: "nod_1", FolderID: "fld_1", AuthorID: &author, ActorID: &actor}
	body, err = Body("evt_2", "comment.deleted", at, "acc_1", nil, CommentDeleted{Comment: withActor, Tombstone: true})
	if err != nil {
		t.Fatal(err)
	}
	got = dataKeys(t, body)
	if strings.Join(got, ",") != "actor_id,author_id,folder_id,id,mentions,node_id,reply_to_id,tombstone" {
		t.Fatalf("comment.deleted keys = %v", got)
	}
	if !strings.Contains(string(body), `"reply_to_id":null`) {
		t.Fatalf("a top-level comment's reply_to_id is not null: %s", body)
	}
}

// dataKeys returns the sorted keys of the data object of an event body.
func dataKeys(t *testing.T, body []byte) []string {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range env.Data {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
