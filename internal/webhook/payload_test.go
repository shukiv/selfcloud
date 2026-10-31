// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"strings"
	"testing"
	"time"
)

func TestBodyEnvelopeIsExact(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 123_000_000, time.UTC)
	ext := "crm-7"
	b, err := Body("evt_1", "file.created", at, "acc_1", &ext, File{ID: "nod_1", FolderID: "fld_1",
		Kind: "file", Size: 12, Name: "a.txt", ParentID: "nod_0"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"evt_1","type":"file.created","time":"2026-10-30T12:00:00.123Z",` +
		`"account":{"id":"acc_1","external_id":"crm-7"},` +
		`"data":{"id":"nod_1","folder_id":"fld_1","kind":"file","size":12,"name":"a.txt","parent_id":"nod_0"}}`
	if string(b) != want {
		t.Fatalf("body\n got %s\nwant %s", b, want)
	}
}

func TestBodyNullExternalID(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	b, err := Body("evt_2", "user.created", at, "acc_1", nil, User{ID: "usr_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"external_id":null`) || !strings.Contains(string(b), `"time":"2026-10-30T12:00:00.000Z"`) {
		t.Fatalf("body %s", b)
	}
}

// A user event names the user by ID and external ID only: never an email address or a name.
func TestBodyUserCarriesNoContactDetails(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	ext := "ext-9"
	b, err := Body("evt_3", "user.verified", at, "acc_1", &ext, User{ID: "usr_9", ExternalID: &ext})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"data":{"id":"usr_9","external_id":"ext-9"}`) {
		t.Fatalf("body %s", b)
	}
}

// upload.completed carries the upload's ID and the file fields at the top of data.
func TestBodyUploadCompletedFlattensFile(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	b, err := Body("evt_4", "upload.completed", at, "acc_1", nil, UploadCompleted{UploadID: "upl_1",
		File: File{ID: "nod_2", FolderID: "fld_1", Kind: "file", Size: 3, Name: "b", ParentID: "nod_1"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `"data":{"upload_id":"upl_1","id":"nod_2","folder_id":"fld_1","kind":"file","size":3,"name":"b","parent_id":"nod_1"}`
	if !strings.Contains(string(b), want) {
		t.Fatalf("body %s", b)
	}
}
