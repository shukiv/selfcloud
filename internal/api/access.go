// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// Access levels, as the API names them. They match the grants.level column.
const (
	accessRead  = store.LevelRead
	accessWrite = store.LevelWrite
)

// accessReader is what the access rules read: the store, or a write transaction. Checks made
// inside a transaction see the grants and users as they are then, not as they were when the
// request arrived.
type accessReader interface {
	Folder(ctx context.Context, accountID, id string) (*store.Folder, error)
	Grant(ctx context.Context, folderID, userID string) (*store.Grant, error)
	User(ctx context.Context, accountID, id string) (*store.User, error)
}

// folderAccessWith returns the folder with the given ID and the level p has on it, read through rd.
// Installation admins, keys and users alike, get forbidden: an admin has no direct path to folder
// contents. To act inside a tenant, an admin creates an account key, and the audit log records
// that creation. Account keys reach every folder of their account, with write. A user is checked
// as the store has it now: a disabled or vanished user is refused, and a member reaches the
// folders it holds a grant on, at the granted level. A folder the caller cannot reach is not_found,
// so folder IDs cannot be probed.
//
// A delegated token narrows that. The folder must be one the token names, and the level is at most
// the token's permission, so a token with read never writes, and a token with write still reads only
// what its user's grant allows. The token cannot widen anything.
func folderAccessWith(ctx context.Context, rd accessReader, p *auth.Principal, folderID string) (*store.Folder, string, error) {
	if p == nil {
		return nil, "", apierr.New(apierr.Unauthenticated, "credentials are required")
	}
	if p.IsAdmin() {
		return nil, "", apierr.New(apierr.Forbidden, "admins manage tenants and may not read their files")
	}
	if p.Token != nil && !slices.Contains(p.Token.Folders, folderID) {
		return nil, "", notFound("folder")
	}

	level := accessWrite
	if p.Kind == auth.KindUser {
		u, err := rd.User(ctx, p.AccountID, p.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if err != nil {
			return nil, "", err
		}
		switch u.Status {
		case store.UserDisabled:
			return nil, "", apierr.New(apierr.UserDisabled, "the user is disabled")
		case store.UserActive:
		default:
			return nil, "", apierr.New(apierr.Unauthenticated, "the session has ended; sign in again")
		}
		if u.Role == store.RoleMember {
			g, err := rd.Grant(ctx, folderID, p.UserID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, "", notFound("folder")
			}
			if err != nil {
				return nil, "", err
			}
			level = g.Level
		}
	}
	if p.Token != nil && !p.Token.Write {
		level = accessRead
	}

	f, err := rd.Folder(ctx, p.AccountID, folderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", notFound("folder")
	}
	if err != nil {
		return nil, "", err
	}
	return f, level, nil
}

// requireAccess is folderAccessWith plus the level check: write demands write. It is the check
// every file operation makes, outside a transaction for the early refusal and inside one for the
// final word.
func requireAccess(ctx context.Context, rd accessReader, p *auth.Principal, folderID string, write bool) (*store.Folder, error) {
	f, level, err := folderAccessWith(ctx, rd, p, folderID)
	if err != nil {
		return nil, err
	}
	if write && level != accessWrite {
		return nil, apierr.New(apierr.Forbidden, "this needs write access to the folder")
	}
	return f, nil
}

// folderAccess is folderAccessWith for the request's caller.
func (s *Server) folderAccess(r *http.Request, folderID string) (*store.Folder, string, error) {
	return folderAccessWith(r.Context(), s.store, principalOf(r), folderID)
}
