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

// Comment is the data of a comment event (decision 83): the comment's ID, the comment it replies to (null for a top-level
// comment), its node and the node's folder, its author, and the actor when someone other than the author made the change
// (absent for the author's own change). Mentions is how many people the change named. It holds IDs and a count, never the
// text, a name or an address. Author is null once the author's user is deleted.
type Comment struct {
	Comment  string  `json:"comment"`
	ReplyTo  *string `json:"reply_to"`
	Node     string  `json:"node"`
	Folder   string  `json:"folder"`
	Author   *string `json:"author"`
	Actor    *string `json:"actor,omitempty"`
	Mentions int     `json:"mentions"`
}

// CommentDeleted is the data of comment.deleted: the comment, and whether the delete left a tombstone that holds its
// replies' thread (decision 39).
type CommentDeleted struct {
	Comment
	Tombstone bool `json:"tombstone"`
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
	// Impersonated is true for a change an installation admin made while signed in as the user (decision 41). The
	// admin's identity is not in the event.
	Impersonated bool `json:"impersonated,omitempty"`
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

// ImpersonatedBody is Body for a change made while an installation admin is signed in as the user: the event says so.
func ImpersonatedBody(eventID, eventType string, at time.Time, accountID string, externalID *string, data any) ([]byte, error) {
	return json.Marshal(envelope{ID: eventID, Type: eventType, Time: at.UTC().Format(timeLayout),
		Account: account{ID: accountID, ExternalID: externalID}, Data: data, Impersonated: true})
}
