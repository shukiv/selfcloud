package api

import (
	"errors"
	"net/http"

	"selfcloud/internal/apierr"
	"selfcloud/internal/auth"
	"selfcloud/internal/store"
)

// Access levels, as the API names them. They match the grants.level column.
const (
	accessRead  = store.LevelRead
	accessWrite = store.LevelWrite
)

// folderAccess returns the folder with the given ID and the level the caller has on it: "read" or
// "write". Every file operation goes through it.
//
// Installation admins, keys and users alike, get forbidden. An admin has no direct path to folder
// contents. To act inside a tenant, an admin creates an account key, and the audit log records
// that creation. Account keys and owner sessions reach
// every folder of their account, with write. A member session reaches the folders it holds a
// grant on, at the granted level. A folder the caller cannot reach is not_found, so folder IDs
// cannot be probed.
func (s *Server) folderAccess(r *http.Request, folderID string) (*store.Folder, string, error) {
	ctx := r.Context()
	p := principalOf(r)
	if p == nil {
		return nil, "", apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return nil, "", apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}

	level := accessWrite
	if p.Kind == auth.KindUser && p.Role == auth.RoleMember {
		g, err := s.store.Grant(ctx, folderID, p.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", notFound("folder")
		}
		if err != nil {
			return nil, "", err
		}
		level = g.Level
	}
	// Slice i: a delegated token narrows level here, to read or to a path prefix, before the
	// check below. A grant or an account key is never widened.

	f, err := s.store.Folder(ctx, p.AccountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", notFound("folder")
	}
	if err != nil {
		return nil, "", err
	}
	return f, level, nil
}
