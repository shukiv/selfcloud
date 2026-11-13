// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// The comments and activity tests use the fake server in files_test.go. Their identifiers have the server's shape.

var (
	testComment  = id.Derive("cmt", "cli11c-comment")
	testReply    = id.Derive("cmt", "cli11c-reply")
	testActivity = id.Derive("act", "cli11c-activity")
)

// commentDoc is a comment document as the API answers it.
func commentDoc(commentID, body string) map[string]any {
	return map[string]any{
		"id": commentID, "body": body, "author_name": "Ada", "created_at": "2026-11-05T09:00:00Z",
		"mentions": []any{}, "resolved": false,
	}
}

// activityDoc is an activity document as the API answers it.
func activityDoc(activityID, action string, details map[string]any) map[string]any {
	return map[string]any{
		"id": activityID, "time": "2026-11-05T09:00:00Z", "kind": "access", "action": action,
		"actor": map[string]any{"kind": "user", "name": "Ada", "mine": false}, "details": details,
	}
}

func TestCommentLsSendsTheNodeAndPrintsTheTable(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := commentDoc(testComment, "hello")
	item["reply_count"] = 2
	item["resolved"] = true
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", item))

	res := f.run(CommentLs, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/comments"))
	want := table(
		[]string{"ID", "AUTHOR", "CREATED", "REPLIES", "RESOLVED", "TEXT"},
		[]string{testComment, "Ada", "2026-11-05T09:00:00Z", "2", "yes", "hello"},
	)
	if res.stdout != want {
		t.Errorf("stdout:\n%s want:\n%s", res.stdout, want)
	}
}

func TestCommentLsRepliesSendsParent(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", commentDoc(testReply, "a reply")))

	res := f.run(CommentLs, testNode, "--replies", testComment)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/comments") + "?parent=" + testComment)
	if !strings.Contains(res.stdout, testReply) || !strings.Contains(res.stdout, "a reply") {
		t.Errorf("stdout %q", res.stdout)
	}
}

func TestCommentLsTombstoneAndCutText(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	tombstone := map[string]any{
		"id": testComment, "deleted": true, "created_at": "2026-11-05T09:00:00Z", "mentions": []any{},
	}
	multiline := commentDoc(testReply, "one line\nand more")
	multiline["replies_more"] = true
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", tombstone, multiline))

	res := f.run(CommentLs, testNode)
	wantCode(t, res, 0)
	want := table(
		[]string{"ID", "AUTHOR", "CREATED", "REPLIES", "RESOLVED", "TEXT"},
		[]string{testComment, "", "2026-11-05T09:00:00Z", "", "", "(deleted)"},
		[]string{testReply, "Ada", "2026-11-05T09:00:00Z", "+", "", "one line…"},
	)
	if res.stdout != want {
		t.Errorf("stdout:\n%s want:\n%s", res.stdout, want)
	}
}

func TestCommentLsCutTextAtEighty(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	long := strings.Repeat("a", 81)
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", commentDoc(testComment, long)))

	res := f.run(CommentLs, testNode)
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, strings.Repeat("a", 80)+"…") {
		t.Errorf("stdout %q", res.stdout)
	}
	if strings.Contains(res.stdout, long) {
		t.Errorf("the whole text was printed: %q", res.stdout)
	}
}

func TestCommentLsPrintsNoEscapeSequence(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", commentDoc(testComment, "evil \x1b[31mred")))

	res := f.run(CommentLs, testNode)
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "\x1b") {
		t.Errorf("stdout has a raw escape: %q", res.stdout)
	}
}

func TestCommentLsJSONDropsUnknownFields(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := commentDoc(testComment, "hello")
	item["token"] = "sck_secret_value"
	reply := commentDoc(testReply, "a reply")
	reply["token"] = "sck_other_secret"
	item["replies"] = []any{reply}
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("", item))

	res := f.run(CommentLs, testNode, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "token") || strings.Contains(res.stdout, "sck_") {
		t.Fatalf("an unknown field was printed: %q", res.stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, res.stdout)
	}
	if len(items) != 1 || items[0]["id"] != testComment {
		t.Fatalf("items %v", items)
	}
	replies, ok := items[0]["replies"].([]any)
	if !ok || len(replies) != 1 {
		t.Fatalf("replies %v", items[0]["replies"])
	}
	first, _ := replies[0].(map[string]any)
	if first["id"] != testReply || first["token"] != nil {
		t.Errorf("reply %v", first)
	}
}

func TestCommentLsRefusesBadArgumentsBeforeRequests(t *testing.T) {
	cases := [][]string{
		{},
		{testNode, testOther},
		{"nod_not-an-id"},
		{testNode, "--replies", "nope"},
	}
	for _, args := range cases {
		isolate(t, testAccountKey)
		f := newFake(t)
		res := f.run(CommentLs, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

func TestCommentLsSaysMoreAndAllFollows(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/comments"), page("c2", commentDoc(testComment, "first")))

	res := f.run(CommentLs, testNode)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "more items exist") {
		t.Errorf("stderr %q", res.stderr)
	}
	f.wantCalls("GET " + nodePath(testNode, "/comments"))
}

func TestCommentLsAllFollowsTheCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/comments"),
		page("c2", commentDoc(testComment, "first")),
		page("", commentDoc(testReply, "second")))

	res := f.run(CommentLs, testNode, "--all")
	wantCode(t, res, 0)
	f.wantCalls(
		"GET "+nodePath(testNode, "/comments"),
		"GET "+nodePath(testNode, "/comments")+"?cursor=c2",
	)
	if !strings.Contains(res.stdout, testComment) || !strings.Contains(res.stdout, testReply) {
		t.Errorf("stdout %q", res.stdout)
	}
}

func TestCommentAddSendsTheBodyAndPrintsIt(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodPost, nodePath(testNode, "/comments"), created(commentDoc(testComment, "hello")))

	res := f.run(CommentAdd, testNode, "--text", "hello")
	wantCode(t, res, 0)
	f.wantCalls("POST " + nodePath(testNode, "/comments"))
	if body := f.wantBody(0); len(body) != 1 || body["body"] != "hello" {
		t.Errorf("body %v", body)
	}
	want := "id=" + testComment + "\nparent_id=\ncreated_at=2026-11-05T09:00:00Z\nauthor_name=Ada\nbody=hello\n"
	if res.stdout != want {
		t.Errorf("stdout %q want %q", res.stdout, want)
	}
}

func TestCommentAddReplySendsParent(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	doc := commentDoc(testReply, "a reply")
	doc["parent_id"] = testComment
	f.on(http.MethodPost, nodePath(testNode, "/comments"), created(doc))

	res := f.run(CommentAdd, testNode, "--reply-to", testComment, "--text", "a reply")
	wantCode(t, res, 0)
	body := f.wantBody(0)
	if body["body"] != "a reply" || body["parent_id"] != testComment {
		t.Errorf("body %v", body)
	}
	if !strings.Contains(res.stdout, "parent_id="+testComment+"\n") {
		t.Errorf("stdout %q", res.stdout)
	}
}

func TestCommentAddReadsAFileAndStandardInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comment.txt")
	if err := os.WriteFile(path, []byte("from a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodPost, nodePath(testNode, "/comments"), created(commentDoc(testComment, "from a file")))
	res := f.run(CommentAdd, testNode, "--text-file", path)
	wantCode(t, res, 0)
	if body := f.wantBody(0); body["body"] != "from a file" {
		t.Errorf("body %v", body)
	}

	isolate(t, testDeviceTok)
	f = newFake(t)
	f.on(http.MethodPost, nodePath(testNode, "/comments"), created(commentDoc(testComment, "from stdin")))
	Stdin = strings.NewReader("from stdin")
	res = f.run(CommentAdd, testNode, "--text-file", "-")
	wantCode(t, res, 0)
	if body := f.wantBody(0); body["body"] != "from stdin" {
		t.Errorf("body %v", body)
	}
}

func TestCommentAddRefusesTextOver64KiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 70*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"a file", []string{testNode, "--text-file", path}, ""},
		{"standard input", []string{testNode, "--text-file", "-"}, strings.Repeat("y", 70*1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t, testDeviceTok)
			Stdin = strings.NewReader(tc.stdin)
			f := newFake(t)
			res := f.run(CommentAdd, tc.args...)
			wantCode(t, res, 2)
			f.wantNoCalls()
			if !strings.Contains(res.stderr, "over 64 KiB") {
				t.Errorf("stderr %q", res.stderr)
			}
		})
	}
}

func TestCommentAddRefusesBadArgumentsBeforeRequests(t *testing.T) {
	cases := [][]string{
		{},
		{testNode},
		{testNode, "--text", "one", "--text-file", "two"},
		{testNode, "--text", "  \n\t"},
		{"nod_not-an-id", "--text", "hello"},
		{testNode, "--reply-to", "nope", "--text", "hello"},
		{testNode, "--text", "hello", "extra"},
	}
	for _, args := range cases {
		isolate(t, testDeviceTok)
		f := newFake(t)
		res := f.run(CommentAdd, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

func TestCommentAddGivesAnAccountKeyTheHintOnlyWhenPlain(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodPost, nodePath(testNode, "/comments"), apiErr(http.StatusForbidden, "forbidden", "comments are off"))

	res := f.run(CommentAdd, testNode, "--text", "hello")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "selfcloud: hint: "+personHint) {
		t.Errorf("stderr %q", res.stderr)
	}

	res = f.run(CommentAdd, testNode, "--text", "hello", "--json")
	wantCode(t, res, 3)
	if strings.Contains(res.stderr, "hint:") {
		t.Errorf("a hint was printed under --json: %q", res.stderr)
	}
}

func TestCommentAddShowsTheServerMessageWhenRateLimited(t *testing.T) {
	isolate(t, testDeviceTok)
	f := newFake(t)
	f.on(http.MethodPost, nodePath(testNode, "/comments"),
		apiErr(http.StatusTooManyRequests, "rate_limited", "slow down"))

	res := f.run(CommentAdd, testNode, "--text", "hello")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "slow down") {
		t.Errorf("stderr %q", res.stderr)
	}
}

func TestActivitySendsTheKindAndPrintsTheTable(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/activity"), page("", activityDoc(testActivity, "permission.set",
		map[string]any{
			"role": "manager", "subject_kind": "user", "subject_name": "Bob", "subject_gone": true,
			"token": "sck_secret_value",
		})))

	res := f.run(Activity, testNode, "--kind", "access")
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/activity") + "?kind=access")
	want := table(
		[]string{"TIME", "ACTOR", "ACTION", "DETAILS"},
		[]string{"2026-11-05T09:00:00Z", "Ada", "permission.set",
			"role=manager subject_kind=user subject_name=Bob subject_gone=true"},
	)
	if res.stdout != want {
		t.Errorf("stdout:\n%s want:\n%s", res.stdout, want)
	}
	if strings.Contains(res.stdout, "token") {
		t.Errorf("an unknown detail was printed: %q", res.stdout)
	}
}

func TestActivityActorFallsBackToItsKind(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := activityDoc(testActivity, "node.create", map[string]any{"kind": "file"})
	item["actor"] = map[string]any{"kind": "system"}
	f.on(http.MethodGet, nodePath(testNode, "/activity"), page("", item))

	res := f.run(Activity, testNode)
	wantCode(t, res, 0)
	f.wantCalls("GET " + nodePath(testNode, "/activity"))
	if !strings.Contains(res.stdout, "system") || !strings.Contains(res.stdout, "kind=file") {
		t.Errorf("stdout %q", res.stdout)
	}
}

func TestActivityJSONDropsUnknownDetailKeys(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/activity"), page("", activityDoc(testActivity, "permission.set",
		map[string]any{"role": "manager", "subject_name": "Bob", "token": "sck_secret_value"})))

	res := f.run(Activity, testNode, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "token") {
		t.Fatalf("an unknown detail was printed: %q", res.stdout)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &items); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, res.stdout)
	}
	if len(items) != 1 {
		t.Fatalf("items %v", items)
	}
	details, _ := items[0]["details"].(map[string]any)
	if details["role"] != "manager" || details["subject_name"] != "Bob" || details["token"] != nil {
		t.Errorf("details %v", details)
	}
	actor, _ := items[0]["actor"].(map[string]any)
	if actor["kind"] != "user" || actor["name"] != "Ada" {
		t.Errorf("actor %v", actor)
	}
}

func TestActivityAllFollowsAnEmptyPage(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on(http.MethodGet, nodePath(testNode, "/activity"),
		page("c2"),
		page("", activityDoc(testActivity, "node.create", map[string]any{"kind": "file"})))

	res := f.run(Activity, testNode, "--kind", "content", "--all")
	wantCode(t, res, 0)
	f.wantCalls(
		"GET "+nodePath(testNode, "/activity")+"?kind=content",
		"GET "+nodePath(testNode, "/activity")+"?cursor=c2&kind=content",
	)
	if !strings.Contains(res.stdout, "node.create") || !strings.Contains(res.stdout, "kind=file") {
		t.Errorf("stdout %q", res.stdout)
	}
}

func TestActivityRefusesBadArgumentsBeforeRequests(t *testing.T) {
	cases := [][]string{
		{},
		{testNode, testOther},
		{"nod_not-an-id"},
		{testNode, "--kind", "everything"},
	}
	for _, args := range cases {
		isolate(t, testAccountKey)
		f := newFake(t)
		res := f.run(Activity, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}
