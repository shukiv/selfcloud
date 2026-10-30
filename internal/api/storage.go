// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Storage (slice p1): each user's limit on its own files, the account's default for it, and the storage
// pages. Owners see per-member totals and team-space files by name; they never see the names of files in a
// member's My files. The account key sees everything. Installation admins see totals and no names.

const (
	storageUsersDefault = 50
	storageUsersMax     = 200
	largestDefault      = 20
	largestMax          = 100
)

// storagePolicyJSON is the account's default storage limit for each user. 0 is no limit.
type storagePolicyJSON struct {
	UserQuotaBytes int64 `json:"user_quota_bytes"`
}

// quotaOrNil is a limit as the API shows it: null when there is none.
func quotaOrNil(limit int64) *int64 {
	if limit <= 0 {
		return nil
	}
	return &limit
}

// availableOf is what a limit leaves free after usage and reservations, or nil when there is no limit.
func availableOf(limit int64, usage store.Usage) *int64 {
	if limit <= 0 {
		return nil
	}
	free := max(0, limit-usage.Bytes-usage.Reserved)
	return &free
}

// effectiveQuota is the limit that applies to a user: the user's own, or the account's default when it follows
// it. nil when there is none.
func effectiveQuota(u *store.User, acc *store.Account) *int64 {
	if u.QuotaBytes != nil {
		return quotaOrNil(*u.QuotaBytes)
	}
	return quotaOrNil(acc.UserQuotaBytes)
}

// storageJSON is the caller's own storage: its My files, against its limit.
type storageJSON struct {
	UsedBytes      int64  `json:"used_bytes"`
	ReservedBytes  int64  `json:"reserved_bytes"`
	QuotaBytes     *int64 `json:"quota_bytes"`
	AvailableBytes *int64 `json:"available_bytes"`
}

// myStorage is the storage of the user whose personal folder is pf. A user without one has none.
func (s *Server) myStorage(r *http.Request, accountID string, pf *store.Folder) (storageJSON, error) {
	if pf == nil {
		return storageJSON{}, nil
	}
	st, err := s.store.QuotaStateOf(r.Context(), accountID, pf.ID)
	if err != nil {
		return storageJSON{}, err
	}
	out := storageJSON{UsedBytes: st.Folder.Bytes, ReservedBytes: st.Folder.Reserved,
		QuotaBytes: quotaOrNil(st.UserLimit)}
	out.AvailableBytes = availableOf(st.UserLimit, st.Folder)
	return out, nil
}

// handleGetStoragePolicy answers the account's default limit for each user.
func (s *Server) handleGetStoragePolicy(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, storagePolicyJSON{UserQuotaBytes: acc.UserQuotaBytes})
	return nil
}

// handleUpdateStoragePolicy sets the account's default limit for each user. 0 is no limit, which is the
// default, so nothing is limited until an owner sets one. A user with its own limit keeps it.
func (s *Server) handleUpdateStoragePolicy(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		UserQuotaBytes *int64 `json:"user_quota_bytes"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.UserQuotaBytes == nil || *in.UserQuotaBytes < 0 {
		return apierr.New(apierr.InvalidRequest, "user_quota_bytes must be a number of bytes, 0 for no limit")
	}
	acc, err := s.writableAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetUserQuotaDefault(ctx, acc.ID, *in.UserQuotaBytes, store.Now()); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "account.storage_policy", acc.ID,
			map[string]any{"user_quota_bytes": *in.UserQuotaBytes}))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, storagePolicyJSON{UserQuotaBytes: *in.UserQuotaBytes})
	return nil
}

// accountUsageJSON is the account's own counters and limit.
type accountUsageJSON struct {
	UsedBytes      int64  `json:"used_bytes"`
	ReservedBytes  int64  `json:"reserved_bytes"`
	QuotaBytes     *int64 `json:"quota_bytes"`
	AvailableBytes *int64 `json:"available_bytes"`
	UserQuotaBytes int64  `json:"user_quota_bytes"`
}

// storageFolderJSON is a folder's counters. Name is set only for a viewer who may see names.
type storageFolderJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Kind          string `json:"kind"`
	UsedBytes     int64  `json:"used_bytes"`
	UsedFiles     int64  `json:"used_files"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

// storageUserJSON is a member's totals. The names are the account's user names, not file names.
type storageUserJSON struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	Email               string `json:"email"`
	Status              string `json:"status"`
	UsedBytes           int64  `json:"used_bytes"`
	ReservedBytes       int64  `json:"reserved_bytes"`
	QuotaBytes          *int64 `json:"quota_bytes"`
	EffectiveQuotaBytes *int64 `json:"effective_quota_bytes"`
}

// accountStorageJSON is GET /accounts/{acc}/storage.
type accountStorageJSON struct {
	Account         accountUsageJSON    `json:"account"`
	Folders         []storageFolderJSON `json:"folders"`
	Users           []storageUserJSON   `json:"users,omitempty"`
	UsersNextCursor string              `json:"users_next_cursor,omitempty"`
}

// storageViewer tells who is looking at an account's storage: the account key, an owner, or an installation
// admin. Anyone else is refused before this is asked.
func storageViewer(p *auth.Principal) (key, owner, admin bool) {
	key = p.Kind == auth.KindAccountKey
	owner = p.Kind == auth.KindUser && p.Role == auth.RoleOwner && p.Token == nil
	admin = p.IsAdmin()
	return key, owner, admin
}

// handleAccountStorage answers the account's totals. The account key and owners also see the users' totals,
// heaviest first, and the folders with their names. Admins see the account's totals and the team spaces' sizes,
// with no names at all.
func (s *Server) handleAccountStorage(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	key, owner, _ := storageViewer(principalOf(r))
	limit := storageUsersDefault
	if v := r.URL.Query().Get("users_limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > storageUsersMax {
			return apierr.Newf(apierr.InvalidRequest, "users_limit must be between 1 and %d", storageUsersMax)
		}
		limit = n
	}
	out := accountStorageJSON{Account: accountUsageJSON{UsedBytes: acc.UsedBytes, ReservedBytes: acc.ReservedBytes,
		UserQuotaBytes: acc.UserQuotaBytes}}
	out.Account.QuotaBytes = quotaOrNil(acc.QuotaBytes)
	if acc.QuotaBytes > 0 {
		out.Account.AvailableBytes = availableOf(acc.QuotaBytes, store.Usage{Bytes: acc.UsedBytes, Reserved: acc.ReservedBytes})
	}
	// An owner's list has the team spaces only: the personal folders are its members' My files, which it sees as totals
	// through the users below. The key's list has every folder. An admin's has the team spaces, with no names.
	teamOnly := owner || !key
	folders, err := s.store.FolderTotals(ctx, acc.ID, teamOnly)
	if err != nil {
		return err
	}
	for _, f := range folders {
		row := storageFolderJSON{ID: f.Folder.ID, Kind: f.Folder.Kind, UsedBytes: f.Usage.Bytes,
			UsedFiles: f.Usage.Files, ReservedBytes: f.Usage.Reserved}
		if key || owner {
			row.Name = f.Folder.Name
		}
		out.Folders = append(out.Folders, row)
	}
	if key || owner {
		page := store.Page{Limit: limit, Cursor: r.URL.Query().Get("users_cursor")}
		users, next, err := s.store.StorageUsers(ctx, acc.ID, page)
		if err != nil {
			return storeErr(err)
		}
		for _, u := range users {
			row := storageUserJSON{ID: u.ID, DisplayName: u.DisplayName, Email: u.Email, Status: u.Status,
				UsedBytes: u.Usage.Bytes, ReservedBytes: u.Usage.Reserved, QuotaBytes: u.QuotaBytes,
				EffectiveQuotaBytes: quotaOrNil(u.EffectiveQuota)}
			out.Users = append(out.Users, row)
		}
		out.UsersNextCursor = next
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// largeFileJSON is one file in a largest-files list.
type largeFileJSON struct {
	ID            string `json:"id"`
	FolderID      string `json:"folder_id,omitempty"`
	FolderName    string `json:"folder_name,omitempty"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	VersionsBytes int64  `json:"versions_bytes"`
	InTrash       bool   `json:"in_trash"`
	ModifiedAt    string `json:"modified_at"`
}

// largeFilesJSON is a page of largest files.
type largeFilesJSON struct {
	Items      []largeFileJSON `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// largestPage reads ?limit= and ?cursor= for a largest-files list.
func largestPage(r *http.Request) (store.Page, error) {
	p := store.Page{Limit: largestDefault, Cursor: r.URL.Query().Get("cursor")}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > largestMax {
			return p, apierr.Newf(apierr.InvalidRequest, "limit must be between 1 and %d", largestMax)
		}
		p.Limit = n
	}
	return p, nil
}

// largeFilesOut turns store rows into the API shape. withFolder adds the folder of each file (team spaces).
func largeFilesOut(files []store.LargeFile, withFolder bool) []largeFileJSON {
	out := make([]largeFileJSON, 0, len(files))
	for _, f := range files {
		row := largeFileJSON{ID: f.ID, Name: f.Name, Size: f.Size, VersionsBytes: f.VersionBytes, InTrash: f.InTrash,
			ModifiedAt: f.ModifiedAt.UTC().Format(time.RFC3339)}
		if withFolder {
			row.FolderID, row.FolderName = f.FolderID, f.FolderName
		}
		out = append(out, row)
	}
	return out
}

// handleAccountLargest lists the largest files of team spaces (owners) or of any folder (the account key). Owners
// never see a member's My files, and admins see no names at all.
func (s *Server) handleAccountLargest(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	key, owner, admin := storageViewer(principalOf(r))
	if admin || (!key && !owner) {
		return apierr.New(apierr.Forbidden, "only the account owner or its key may list the largest files")
	}
	page, err := largestPage(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	folderID := r.URL.Query().Get("folder")
	kind := ""
	if owner && !key {
		kind = store.KindTeam
	}
	if folderID != "" {
		f, err := s.store.Folder(ctx, acc.ID, folderID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("folder")
		}
		if err != nil {
			return err
		}
		if owner && !key && f.Kind == store.KindPersonal {
			return apierr.New(apierr.Forbidden, "a member's My files are not listed to the owner")
		}
	}
	files, next, err := s.store.LargestFiles(ctx, acc.ID, folderID, kind, page)
	if err != nil {
		return storeErr(err)
	}
	writeJSON(w, http.StatusOK, largeFilesJSON{Items: largeFilesOut(files, true), NextCursor: next})
	return nil
}

// handleMyLargest lists the caller's own largest files: its My files, with their trash marks.
func (s *Server) handleMyLargest(w http.ResponseWriter, r *http.Request) error {
	p := principalOf(r)
	ctx := r.Context()
	page, err := largestPage(r)
	if err != nil {
		return err
	}
	pf, err := s.store.PersonalFolder(ctx, p.AccountID, p.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, largeFilesJSON{Items: []largeFileJSON{}})
		return nil
	}
	if err != nil {
		return err
	}
	files, next, err := s.store.LargestFiles(ctx, p.AccountID, pf.ID, "", page)
	if err != nil {
		return storeErr(err)
	}
	writeJSON(w, http.StatusOK, largeFilesJSON{Items: largeFilesOut(files, false), NextCursor: next})
	return nil
}

// quotaRaw reads the quota_bytes of a user PATCH: absent (nil), null (the account default), or a number.
// The second result is whether the field was given at all.
func quotaRaw(raw []byte) (*int64, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if string(raw) == "null" {
		return nil, true, nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || n < 0 {
		return nil, true, apierr.New(apierr.InvalidRequest, "quota_bytes must be a number of bytes, 0 for no limit, or null for the account default")
	}
	return &n, true, nil
}

// pfOrNil is the personal folder of a lookup, or nil when there is none.
func pfOrNil(pf *store.Folder, err error) *store.Folder {
	if err != nil {
		return nil
	}
	return pf
}
