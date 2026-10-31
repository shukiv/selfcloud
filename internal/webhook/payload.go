// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import (
	"encoding/json"
	"time"
)

// timeLayout is how an event's time is written: UTC, with milliseconds.
const timeLayout = "2006-01-02T15:04:05.000Z"

// File is the data of a file event: the node as the change left it (design section 10). It holds the node's
// ID, folder, kind, size, name and parent, and nothing else. It never holds contents, a signed link, or the
// target of a shortcut.
type File struct {
	ID       string `json:"id"`
	FolderID string `json:"folder_id"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

// Folder is the data of a folder summary: a change touched more files under the folder than one event each may name.
// It holds the folder's own IDs and kind, and subtree true. It names no file; the receiver re-reads the folder through
// the API.
type Folder struct {
	ID       string `json:"id"`
	FolderID string `json:"folder_id"`
	Kind     string `json:"kind"`
	ParentID string `json:"parent_id"`
	Subtree  bool   `json:"subtree"`
}

// Quota is the data of quota.warning and quota.exceeded (decision 30): the scope the level was crossed in, the scope's
// ID, the level, and the numbers. Scope is "account", or "user" for a user's limit on its personal folder. Nothing
// names a file or a folder path.
type Quota struct {
	Scope      string `json:"scope"`
	ID         string `json:"id"`
	Percent    int64  `json:"percent"`     // the level as a share of the limit: the account's warning percent, or 100
	UsedBytes  int64  `json:"used_bytes"`  // what the quota counts: used bytes plus reserved bytes
	LimitBytes int64  `json:"limit_bytes"` // the limit the share is taken of
}

// User is the data of a user event: the user's ID and its external ID. No email address and no name.
type User struct {
	ID         string  `json:"id"`
	ExternalID *string `json:"external_id"`
}

// UploadCompleted is the data of upload.completed: the upload and the file it made or replaced.
type UploadCompleted struct {
	UploadID string `json:"upload_id"`
	File
}

// envelope is the body of every delivery. The store keeps the bytes as they are sent, so the signature
// covers exactly what the receiver reads.
type envelope struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Time    string  `json:"time"`
	Account account `json:"account"`
	Data    any     `json:"data"`
}

// account names the account the event belongs to: its ID, and its external ID when it has one.
type account struct {
	ID         string  `json:"id"`
	ExternalID *string `json:"external_id"`
}

// Body returns the JSON body of an event: the envelope, with data as its data. externalID is nil when the
// account has no external ID.
func Body(eventID, eventType string, at time.Time, accountID string, externalID *string, data any) ([]byte, error) {
	return json.Marshal(envelope{ID: eventID, Type: eventType, Time: at.UTC().Format(timeLayout),
		Account: account{ID: accountID, ExternalID: externalID}, Data: data})
}
