// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// The transfers keep small records beside the profiles, under transfers/ in the profiles directory: one file per
// upload this client began (uploads/<upl_id>.json), so that put --resume and upload ls find it, and one per download
// in progress (downloads/<key>.json), which ties a part file to the version of the file it holds. A record holds ids,
// local paths, sizes and times, never a credential, a share link's secret or a password. Each file is written whole to
// a new private temporary file and renamed into place, and the directories are private, as the profiles' are.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/profile"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
)

const (
	recordVersion = 1
	// maxRecord bounds a record read from disk. A record is well under a kilobyte.
	maxRecord     = 64 << 10
	uploadsKind   = "uploads"
	downloadsKind = "downloads"
)

// stateDir returns the directory of one kind of record. With create, missing directories are made with mode 0700;
// without, a missing one is "" and no error. The profiles directory and each directory under it must be private.
func stateDir(kind string, create bool) (string, error) {
	base, err := profile.DefaultDir()
	if err != nil {
		return "", err
	}
	dirs := []string{base, filepath.Join(base, "transfers"), filepath.Join(base, "transfers", kind)}
	for _, d := range dirs {
		ok, err := privateDir(d, create)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
	}
	return dirs[len(dirs)-1], nil
}

// privateDir checks a directory, and makes it with mode 0700 when it is missing and create is set. It reports false for
// a missing directory it did not make. A directory that another user may read or enter is refused, with the chmod.
func privateDir(dir string, create bool) (bool, error) {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return false, nil
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, fmt.Errorf("cannot make %s: %w", dir, err)
		}
		fi, err = os.Stat(dir)
	}
	if err != nil {
		return false, fmt.Errorf("cannot read %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return false, exitcode.Usagef("%s is not a directory", dir)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return false, exitcode.Usagef("%s has permissions %04o; it holds the profiles and the transfers' records, so "+
			"only you may use it: run chmod 700 %s", dir, perm, dir)
	}
	return true, nil
}

// writeRecord writes v as name in dir: a new temporary file, created with O_EXCL and mode 0600, written, synced and
// renamed over name. A failure before the rename leaves the old record whole.
func writeRecord(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+hex.EncodeToString(b)+".tmp")
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// readRecord reads one record file strictly into v: a private regular file, no larger than maxRecord, holding one JSON
// object with no unknown field.
func readRecord(path string, v any) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 || fi.Size() > maxRecord {
		return errors.New("not a private record file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("more than one value")
	}
	return nil
}

// uploadRecord is one upload this client began. Server is the address it was begun on; Sent is the offset the server
// last acknowledged; LocalModified and Size identify the local file, so a resume refuses a file that changed.
type uploadRecord struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	Server        string    `json:"server"`
	Profile       string    `json:"profile,omitempty"`
	Local         string    `json:"local"`
	Folder        string    `json:"folder"`
	Path          string    `json:"path,omitempty"`
	Parent        string    `json:"parent,omitempty"`
	Name          string    `json:"name"`
	OnConflict    string    `json:"on_conflict"`
	Size          int64     `json:"size"`
	Sent          int64     `json:"sent"`
	StartedAt     time.Time `json:"started_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	LocalModified time.Time `json:"local_modified"`
}

// valid checks a record read from disk, field by field.
func (r uploadRecord) valid() bool {
	if r.Version != recordVersion || !id.Valid("upl", r.ID) || !id.Valid("fld", r.Folder) ||
		!filepath.IsAbs(r.Local) || r.Size < 0 || r.Sent < 0 || r.Sent > r.Size {
		return false
	}
	if _, err := client.ParseBaseURL(r.Server, true); err != nil {
		return false
	}
	if r.Parent != "" && (!id.Valid("nod", r.Parent) || r.Path != "") {
		return false
	}
	if r.Profile != "" && profile.ValidName(r.Profile) != nil {
		return false
	}
	if _, _, err := names.Clean(r.Name); err != nil {
		return false
	}
	switch r.OnConflict {
	case "fail", "rename", "replace":
	default:
		return false
	}
	return true
}

// saveUploadRecord writes the record of an upload, as <id>.json.
func saveUploadRecord(r uploadRecord) error {
	if !id.Valid("upl", r.ID) {
		return errors.New("an upload record needs the upload's id")
	}
	dir, err := stateDir(uploadsKind, true)
	if err != nil {
		return err
	}
	r.Version = recordVersion
	r.StartedAt, r.ExpiresAt, r.LocalModified = r.StartedAt.UTC(), r.ExpiresAt.UTC(), r.LocalModified.UTC()
	return writeRecord(dir, r.ID+".json", r)
}

// errNoRecord is a record that is not there.
var errNoRecord = errors.New("no record")

// loadUploadRecord reads the record of the upload uplID.
func loadUploadRecord(uplID string) (uploadRecord, error) {
	var r uploadRecord
	dir, err := stateDir(uploadsKind, false)
	if err != nil {
		return r, err
	}
	if dir == "" {
		return r, errNoRecord
	}
	err = readRecord(filepath.Join(dir, uplID+".json"), &r)
	if errors.Is(err, fs.ErrNotExist) {
		return r, errNoRecord
	}
	if err != nil || !r.valid() || r.ID != uplID {
		return r, exitcode.Usagef("the record of upload %s could not be read; cancel the upload with upload cancel", uplID)
	}
	return r, nil
}

// forgetUpload removes the record of an upload. A record that is not there is no error.
func forgetUpload(uplID string) error {
	dir, err := stateDir(uploadsKind, false)
	if err != nil || dir == "" {
		return err
	}
	err = os.Remove(filepath.Join(dir, uplID+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// listUploadRecords reads every upload record, oldest first. bad counts the record files that could not be read.
func listUploadRecords() (recs []uploadRecord, bad int, err error) {
	dir, err := stateDir(uploadsKind, false)
	if err != nil || dir == "" {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		uplID, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !id.Valid("upl", uplID) {
			continue
		}
		var r uploadRecord
		if err := readRecord(filepath.Join(dir, e.Name()), &r); err != nil || !r.valid() || r.ID != uplID {
			bad++
			continue
		}
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool {
		if !recs[i].StartedAt.Equal(recs[j].StartedAt) {
			return recs[i].StartedAt.Before(recs[j].StartedAt)
		}
		return recs[i].ID < recs[j].ID
	})
	return recs, bad, nil
}

// downloadRecord ties a part file to the file and the version its bytes are from, so a later get continues it only
// with that version.
type downloadRecord struct {
	Version   int       `json:"version"`
	Part      string    `json:"part"`
	Server    string    `json:"server"`
	Node      string    `json:"node"`
	ETag      string    `json:"etag"`
	Size      int64     `json:"size"`
	StartedAt time.Time `json:"started_at"`
}

// downloadKey names the record of a part file by its absolute path.
func downloadKey(part string) string {
	s := sha256.Sum256([]byte(part))
	return hex.EncodeToString(s[:16]) + ".json"
}

// loadDownloadRecord reads the record of the part file part, or errNoRecord.
func loadDownloadRecord(part string) (downloadRecord, error) {
	var r downloadRecord
	dir, err := stateDir(downloadsKind, false)
	if err != nil {
		return r, err
	}
	if dir == "" {
		return r, errNoRecord
	}
	err = readRecord(filepath.Join(dir, downloadKey(part)), &r)
	named := id.Valid("nod", r.Node) || id.Valid("zip", r.Node)
	if err != nil || r.Version != recordVersion || r.Part != part || !named || !validETag(r.ETag) {
		return r, errNoRecord
	}
	return r, nil
}

func saveDownloadRecord(r downloadRecord) error {
	dir, err := stateDir(downloadsKind, true)
	if err != nil {
		return err
	}
	r.Version = recordVersion
	r.StartedAt = r.StartedAt.UTC()
	return writeRecord(dir, downloadKey(r.Part), r)
}

// forgetDownload removes the record of a part file. A record that is not there is no error.
func forgetDownload(part string) error {
	dir, err := stateDir(downloadsKind, false)
	if err != nil || dir == "" {
		return err
	}
	err = os.Remove(filepath.Join(dir, downloadKey(part)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// validETag accepts a strong entity tag of printable ASCII, the form the server gives a file's version.
func validETag(t string) bool {
	if len(t) < 2 || len(t) > 128 || t[0] != '"' || t[len(t)-1] != '"' {
		return false
	}
	for i := 1; i < len(t)-1; i++ {
		if t[i] <= ' ' || t[i] > '~' || t[i] == '"' {
			return false
		}
	}
	return true
}
