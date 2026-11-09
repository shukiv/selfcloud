// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// notUTF8 is a JSON-looking body with a byte that never starts a UTF-8 sequence. nulText has a NUL byte.
const (
	notUTF8 = "{\"a\": \"\xff\"}"
	nulText = "{\"a\": \"\x00\"}"
)

// createFile creates a file through POST with the given Content-Type, so the server takes the stored type from it.
func createFile(t *testing.T, s *Server, key, fld, p, contentType, body string) nodeJSON {
	t.Helper()
	rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q(p), key, body,
		map[string]string{"Content-Type": contentType})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// storeFileAs writes a file straight to the store with the stored media type given. An upload or a POST takes the type
// from the name, and this machine's MIME table maps some listed extensions (.yaml) to a text type, so a stored
// application/octet-stream file, which the name rule must cover, cannot always be made over HTTP.
func storeFileAs(t *testing.T, fx *sharingFixture, name, mimeType, body string) nodeJSON {
	t.Helper()
	ctx := context.Background()
	wr, err := fx.s.blobs.Create(ctx, fx.acc.ID, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(wr, body); err != nil {
		t.Fatal(err)
	}
	c, err := wr.Commit()
	if err != nil {
		t.Fatal(err)
	}
	root, err := fx.s.store.RootNode(ctx, fx.acc.ID, fx.fld.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	file := &store.Node{ID: id.New("nod"), AccountID: fx.acc.ID, FolderID: fx.fld.ID, ParentID: root.ID,
		Name: name, NameKey: name, Kind: store.KindFile, Size: c.Size, MimeType: mimeType, BlobID: c.ID,
		ETag: newETag(), SHA256: c.SHA256, CreatedAt: now, ModifiedAt: now}
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: fx.acc.ID, Size: c.Size, SHA256: c.SHA256,
			State: store.BlobLive, CreatedAt: now}); err != nil {
			return err
		}
		return tx.InsertNode(ctx, file)
	}); err != nil {
		t.Fatal(err)
	}
	return decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+file.ID, fx.key, nil))
}

// putContentAs replaces a file's content through the real handler, with the If-Match of the read and the given
// Content-Type.
func putContentAs(t *testing.T, fx *sharingFixture, n nodeJSON, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	return rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+n.ID+"/content", fx.key, body,
		map[string]string{"If-Match": n.ETag, "Content-Type": contentType})
}

// wantContent asks the file to hold want, read through the API.
func wantContent(t *testing.T, fx *sharingFixture, nodeID, want string) {
	t.Helper()
	if got := fileBody(t, fx.s, fx.key, nodeID); got != want {
		t.Errorf("contents = %q, want %q", got, want)
	}
}

// wantRefusedUTF8 asks a refusal to be the 400 that names the rule, and the file to be unchanged: same ETag, same
// contents as before the PUT.
func wantRefusedUTF8(t *testing.T, fx *sharingFixture, rec *httptest.ResponseRecorder, before nodeJSON, content string) {
	t.Helper()
	d := wantError(t, rec, 400, apierr.InvalidRequest)
	if d.Message != "the file must be UTF-8 text" {
		t.Errorf("message = %q", d.Message)
	}
	after := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+before.ID, fx.key, nil))
	if after.ETag != before.ETag {
		t.Errorf("a refused replacement changed the ETag")
	}
	wantContent(t, fx, before.ID, content)
}

// TestPutRefusesNonUTF8ToTextFiles: a replacement of a text-named file whose body is not UTF-8 text is refused with 400,
// and the file is unchanged. Accepted bodies are kept as sent (decision 113).
func TestPutRefusesNonUTF8ToTextFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		make func(*testing.T, *sharingFixture) nodeJSON
		ct   string
		body string
		want int
	}{
		{name: "json with invalid UTF-8", make: makeJSON, ct: "application/json", body: notUTF8, want: 400},
		{name: "json with a NUL byte", make: makeJSON, ct: "application/json", body: nulText, want: 400},
		{name: "yaml stored as octet-stream is matched by name", make: makeYAMLOctet,
			ct: "application/octet-stream", body: notUTF8, want: 400},
		{name: "upper-case extension is matched too", make: makeUpperINI,
			ct: "application/octet-stream", body: notUTF8, want: 400},
		{name: "Dockerfile is matched by its whole name", make: makeDockerfile,
			ct: "application/octet-stream", body: notUTF8, want: 400},
		{name: "json with a BOM is accepted", make: makeJSON, ct: "application/json",
			body: "\xef\xbb\xbf{}", want: 200},
		{name: "json with valid UTF-8 is accepted", make: makeJSON, ct: "application/json",
			body: "{\"name\": \"héllo\"}", want: 200},
		{name: "ts stored as MPEG-TS video takes binary", make: makeMPEGTS, ct: "video/mp2t",
			body: "\x47\x40\x00\x10\x00\x00", want: 200},
		{name: "txt stored as a PNG takes any body", make: makePNGText, ct: "image/png",
			body: notUTF8 + nulText, want: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newSharingFixture(t)
			before := tc.make(t, fx)
			content := fileBody(t, fx.s, fx.key, before.ID)
			rec := putContentAs(t, fx, before, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 400 {
				wantRefusedUTF8(t, fx, rec, before, content)
				return
			}
			wantContent(t, fx, before.ID, tc.body)
		})
	}
}

func makeJSON(t *testing.T, fx *sharingFixture) nodeJSON {
	return createFile(t, fx.s, fx.key, fx.fld.ID, "/data.json", "application/json", "{}")
}

func makeYAMLOctet(t *testing.T, fx *sharingFixture) nodeJSON {
	return storeFileAs(t, fx, "config.yaml", "application/octet-stream", "a: 1")
}

func makeUpperINI(t *testing.T, fx *sharingFixture) nodeJSON {
	return storeFileAs(t, fx, "CONFIG.INI", "application/octet-stream", "a=1")
}

func makeDockerfile(t *testing.T, fx *sharingFixture) nodeJSON {
	return storeFileAs(t, fx, "Dockerfile", "application/octet-stream", "FROM scratch\n")
}

func makeMPEGTS(t *testing.T, fx *sharingFixture) nodeJSON {
	return createFile(t, fx.s, fx.key, fx.fld.ID, "/clip.ts", "video/mp2t", "\x47\x40\x00\x10\x00\x00")
}

func makePNGText(t *testing.T, fx *sharingFixture) nodeJSON {
	return createFile(t, fx.s, fx.key, fx.fld.ID, "/pic.txt", "image/png", "\x89PNG\r\n")
}

// TestPutRequestContentTypeTextIsChecked: a PUT whose Content-Type is text/* is checked too, for a name the code list
// does not know and a stored type that is not text. The same bytes sent as octet-stream are accepted.
func TestPutRequestContentTypeTextIsChecked(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	n := storeFileAs(t, fx, "blob.bin", "application/octet-stream", "old")
	rec := putContentAs(t, fx, n, "text/plain", notUTF8)
	wantRefusedUTF8(t, fx, rec, n, "old")

	wantStatus(t, putContentAs(t, fx, n, "application/octet-stream", notUTF8), 200)
	wantContent(t, fx, n.ID, notUTF8)
}

// TestPutPreconditionsStillComeFirst: without If-Match, with an unquoted one, or with a stale one, the answers are 428,
// 400 and 412, whatever the body. The file is unchanged.
func TestPutPreconditionsStillComeFirst(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	n := makeJSON(t, fx)
	url := "/api/v1/nodes/" + n.ID + "/content"
	hdr := map[string]string{"Content-Type": "application/json"}

	wantError(t, rawCall(t, fx.s, "PUT", url, fx.key, notUTF8, hdr), 428, apierr.PreconditionRequired)
	hdr["If-Match"] = strings.Trim(n.ETag, `"`)
	wantError(t, rawCall(t, fx.s, "PUT", url, fx.key, notUTF8, hdr), 400, apierr.InvalidRequest)
	hdr["If-Match"] = `"stale"`
	wantError(t, rawCall(t, fx.s, "PUT", url, fx.key, notUTF8, hdr), 412, apierr.PreconditionFailed)
	wantContent(t, fx, n.ID, "{}")
}

// TestPutOnALockedTextFileIsStill409: an office editor holds a text file, so the replacement is refused as locked. The
// body is valid UTF-8, so the answer is the lock's, and the file is unchanged.
func TestPutOnALockedTextFileIsStill409(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	n := createFile(t, fx.s, fx.key, fx.fld.ID, "/notes.json", "application/json", "{}")
	fx.grant(t, n.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, n.ID, "edit")
	wantStatus(t, rec, 200)
	tok := decode[officeJSON](t, rec).Token
	fx.lockNodeFor(t, n.ID, tok, "L1")

	wantStatus(t, apiPut(t, fx.s, fx.key, n.ID, n.ETag, "{\"b\": 1}"), 409)
	if got := fx.fileText(t, n.ID); got != "{}" {
		t.Errorf("a refused replacement changed the file to %q", got)
	}
}

// TestWOPIPutFileIsNotChecked: a WOPI PutFile on a text-named file is accepted with any bytes. The rule sits in the PUT
// handler, not in replaceContent, which PutFile also calls.
func TestWOPIPutFileIsNotChecked(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	n := createFile(t, fx.s, fx.key, fx.fld.ID, "/letter.json", "application/json", "{}")
	fx.grant(t, n.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, n.ID, "edit")
	wantStatus(t, rec, 200)
	tok := decode[officeJSON](t, rec).Token
	fx.lockNodeFor(t, n.ID, tok, "L1")

	body := notUTF8 + nulText
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(n.ID)+"/contents", tok, strings.NewReader(body), saveHdr("L1")), 200)
	if got := fx.fileText(t, n.ID); got != body {
		t.Errorf("PutFile left %q", got)
	}
}

// TestUploadOfNonUTF8IsNotChecked: a resumable upload to a text-named file is accepted with any bytes. An upload is not
// a replacement, so the rule does not reach it.
func TestUploadOfNonUTF8IsNotChecked(t *testing.T) {
	t.Parallel()
	s, key, _, f := uploadSetup(t)
	data := notUTF8 + nulText
	upl := uploadID(t, createUpload(t, s, key, int64(len(data)), md("folder", f.ID, "name", "data.json", "sha256", digest(data))))
	rec := piece(t, s, key, upl, 0, data, nil)
	wantStatus(t, rec, 204)
	if got := fileBody(t, s, key, rec.Header().Get(nodeHeader)); got != data {
		t.Errorf("upload left %q", got)
	}
}
