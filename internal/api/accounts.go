// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// jobAccountDelete removes an account and everything in it.
const jobAccountDelete = "account.delete"

type accountJSON struct {
	ID         string    `json:"id"`
	ExternalID *string   `json:"external_id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	QuotaBytes int64     `json:"quota_bytes"`
	UsedBytes  int64     `json:"used_bytes"`
	UsedFiles  int64     `json:"used_files"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toAccountJSON(a *store.Account) accountJSON {
	out := accountJSON{
		ID: a.ID, Name: a.Name, Status: a.Status, QuotaBytes: a.QuotaBytes,
		UsedBytes: a.UsedBytes, UsedFiles: a.UsedFiles, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if a.ExternalID != "" {
		out.ExternalID = &a.ExternalID
	}
	return out
}

// ownerInput is the owner of a new account, created in the same call. Its verification is one of
// email, link or none, as for any user.
type ownerInput struct {
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Locale       string `json:"locale"`
	Verification string `json:"verification"`
}

// accountResponse is an account, with the owner made with it when one was.
type accountResponse struct {
	accountJSON
	Owner *userResponse `json:"owner,omitempty"`
}

// handleCreateAccount creates an account, and with "owner" its first owner user, in one
// transaction. With an external_id, repeating the same request returns the existing account (200)
// instead of creating another, so retries are safe. A retry does not compare the owner and does
// not return one, since the owner was made by the first call.
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Name       string      `json:"name"`
		ExternalID string      `json:"external_id"`
		QuotaBytes int64       `json:"quota_bytes"`
		Owner      *ownerInput `json:"owner"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	name, err := cleanLabel("name", in.Name, 200)
	if err != nil {
		return err
	}
	ext, err := cleanExternalID(in.ExternalID)
	if err != nil {
		return err
	}
	if in.QuotaBytes < 0 {
		return apierr.New(apierr.InvalidRequest, "quota_bytes may not be negative")
	}
	var owner *store.User
	if in.Owner != nil {
		if owner, err = s.newOwner(r, in.Owner); err != nil {
			return err
		}
	}

	ctx := r.Context()
	var (
		out    *store.Account
		issued *issuedToken
	)
	created := false
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if ext != "" {
			existing, err := tx.AccountByExternalID(ctx, ext)
			switch {
			case err == nil && existing.Name == name && existing.QuotaBytes == in.QuotaBytes &&
				existing.Status != store.AccountDeleting:
				out = existing
				return nil
			case err == nil:
				return apierr.New(apierr.Conflict, "an account with this external_id exists with different fields").
					With("id", existing.ID)
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		now := store.Now()
		out = &store.Account{ID: id.New("acc"), ExternalID: ext, Name: name, Status: store.AccountActive,
			QuotaBytes: in.QuotaBytes, CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertAccount(ctx, out); err != nil {
			return err
		}
		created = true
		if err := tx.InsertAudit(ctx, s.auditEntry(r, out.ID, "account.create", out.ID,
			map[string]any{"name": name, "external_id": ext, "quota_bytes": in.QuotaBytes})); err != nil {
			return err
		}
		if owner == nil {
			return nil
		}
		owner.AccountID, owner.CreatedAt, owner.UpdatedAt = out.ID, now, now
		if in.Owner.Verification == methodEmail {
			if err := s.checkMailBudget(out.ID, owner.Email); err != nil {
				return err
			}
		}
		issued, err = s.addUser(ctx, r, tx, owner, in.Owner.Verification, "user.create", now)
		return err
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	resp := accountResponse{accountJSON: toAccountJSON(out)}
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/accounts/"+out.ID)
		if owner != nil {
			ur := userResponse{userJSON: toUserJSON(owner)}
			if issued != nil {
				ur.Verification = s.deliver(ctx, r, owner, issued, in.Owner.Verification)
			}
			resp.Owner = &ur
		}
	}
	writeJSON(w, status, resp)
	return nil
}

// newOwner checks an owner given with a new account and returns it as a user, not yet saved.
func (s *Server) newOwner(r *http.Request, in *ownerInput) (*store.User, error) {
	email, err := cleanEmail(in.Email)
	if err != nil {
		return nil, err
	}
	displayName, err := cleanOptionalLabel("display_name", in.DisplayName, 200)
	if err != nil {
		return nil, err
	}
	locale, err := cleanLocale(in.Locale)
	if err != nil {
		return nil, err
	}
	switch in.Verification {
	case methodEmail, methodLink, methodNone:
	default:
		return nil, apierr.New(apierr.InvalidRequest, "owner verification must be email, link or none")
	}
	if err := s.checkVerificationMethod(r, in.Verification, linkTarget{role: store.RoleOwner, status: store.UserPending}); err != nil {
		return nil, err
	}
	return &store.User{ID: id.New("usr"), Email: email, EmailKey: emailKey(email), DisplayName: displayName,
		Locale: locale, Role: store.RoleOwner}, nil
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	filter := store.AccountFilter{ExternalID: q.Get("external_id"), Query: strings.TrimSpace(q.Get("q"))}
	if len([]rune(filter.Query)) > 200 {
		return apierr.New(apierr.InvalidRequest, "q may be at most 200 characters")
	}
	switch status := q.Get("status"); status {
	case "":
	case store.AccountActive, store.AccountSuspended:
		filter.Status = status
	default:
		return apierr.New(apierr.InvalidRequest, "status must be active or suspended")
	}
	accounts, next, err := s.store.AccountsFiltered(r.Context(), filter, page)
	if err != nil {
		return storeErr(err)
	}
	out := listResponse[accountJSON]{Items: []accountJSON{}, NextCursor: next}
	for _, a := range accounts {
		out.Items = append(out.Items, toAccountJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toAccountJSON(acc))
	return nil
}

// handleUpdateAccount changes the fields given. A quota below current usage is allowed: files
// already stored stay, and new writes are refused until usage drops.
func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Name       *string `json:"name"`
		ExternalID *string `json:"external_id"` // "" clears it
		QuotaBytes *int64  `json:"quota_bytes"`
		Status     *string `json:"status"` // active or suspended
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var out *store.Account
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		acc, err := tx.Account(ctx, r.PathValue("acc"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("account")
		}
		if err != nil {
			return err
		}
		if acc.Status == store.AccountDeleting {
			return apierr.New(apierr.Conflict, "the account is being deleted")
		}
		changed := map[string]any{}
		if in.Name != nil {
			if acc.Name, err = cleanLabel("name", *in.Name, 200); err != nil {
				return err
			}
			changed["name"] = acc.Name
		}
		if in.ExternalID != nil {
			if acc.ExternalID, err = cleanExternalID(*in.ExternalID); err != nil {
				return err
			}
			changed["external_id"] = acc.ExternalID
		}
		if in.QuotaBytes != nil {
			if *in.QuotaBytes < 0 {
				return apierr.New(apierr.InvalidRequest, "quota_bytes may not be negative")
			}
			acc.QuotaBytes = *in.QuotaBytes
			changed["quota_bytes"] = acc.QuotaBytes
		}
		if in.Status != nil {
			if *in.Status != store.AccountActive && *in.Status != store.AccountSuspended {
				return apierr.New(apierr.InvalidRequest, "status must be active or suspended")
			}
			acc.Status = *in.Status
			changed["status"] = acc.Status
		}
		acc.UpdatedAt = store.Now()
		if err := tx.UpdateAccount(ctx, acc); errors.Is(err, store.ErrConflict) {
			return apierr.New(apierr.Conflict, "another account has this external_id")
		} else if err != nil {
			return err
		}
		out = acc
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "account.update", acc.ID, changed))
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, toAccountJSON(out))
	return nil
}

// handleDeleteAccount marks the account deleting, which stops all access at once, and queues
// a job that removes it. Repeating the call returns the same job.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	ctx := r.Context()
	var job *store.Job
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		acc, err := tx.Account(ctx, r.PathValue("acc"))
		if errors.Is(err, store.ErrNotFound) {
			return notFound("account")
		}
		if err != nil {
			return err
		}
		if acc.Status == store.AccountDeleting {
			if job, err = tx.UnfinishedJob(ctx, acc.ID, jobAccountDelete, acc.ID); err == nil {
				return nil
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		now := store.Now()
		acc.Status = store.AccountDeleting
		acc.UpdatedAt = now
		if err := tx.UpdateAccount(ctx, acc); err != nil {
			return err
		}
		job = &store.Job{ID: id.New("job"), AccountID: acc.ID, Kind: jobAccountDelete, Target: acc.ID,
			Status: store.JobQueued, CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertJob(ctx, job); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc.ID, "account.delete", acc.ID, map[string]any{"job": job.ID}))
	})
	if err != nil {
		return err
	}
	s.jobs.Wake()
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, toJobJSON(job))
	return nil
}

// runAccountDelete removes a deleting account from the database. Safe to repeat.
//
// The database rows go first, in one transaction; the account's files are then removed from disk.
// A repeat finds nothing left to delete and only removes the files.
func (s *Server) runAccountDelete(ctx context.Context, job *store.Job) error {
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteAccount(ctx, job.Target); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, &store.AuditEntry{Time: store.Now(), AccountID: job.Target,
			ActorKind: "system", ActorID: job.ID, Action: "account.deleted", Target: job.Target})
	})
	if err != nil {
		return err
	}
	if err := s.blobs.RemoveAccount(job.Target); err != nil {
		return err
	}
	// The account's thumbnails go with its blobs. Their rows went with the account, in the transaction above.
	if err := s.zipFiles.RemoveAccount(job.Target); err != nil {
		return err
	}
	return s.derivedPool.Cache().Files().RemoveAccount(job.Target)
}

// usageJSON is an account's usage, as GET /accounts/{acc}/usage returns it. The quota and the
// available bytes are null when the account has no quota.
type usageJSON struct {
	QuotaBytes     *int64 `json:"quota_bytes"`     // null when unlimited
	UsedBytes      int64  `json:"used_bytes"`      // the sizes of its files, copies included
	ReservedBytes  int64  `json:"reserved_bytes"`  // the declared lengths of its unfinished uploads
	AvailableBytes *int64 `json:"available_bytes"` // quota less used and reserved; null when unlimited
	Files          int64  `json:"files"`           // how many files it holds
	Folders        int64  `json:"folders"`         // its top-level folders
	UsedFiles      int64  `json:"used_files"`      // the same as files; kept for clients that read it
	UsersActive    int64  `json:"users_active"`    // its users who may sign in
	UsersPending   int64  `json:"users_pending"`   // its users who have not verified yet
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	folders, err := s.store.CountFolders(r.Context(), acc.ID)
	if err != nil {
		return err
	}
	out := usageJSON{UsedBytes: acc.UsedBytes, ReservedBytes: acc.ReservedBytes, Files: acc.UsedFiles,
		Folders: folders, UsedFiles: acc.UsedFiles}
	if out.UsersActive, out.UsersPending, err = s.store.PeopleCounts(r.Context(), acc.ID); err != nil {
		return err
	}
	if acc.QuotaBytes > 0 {
		quota := acc.QuotaBytes
		out.QuotaBytes = &quota
		avail := max(0, acc.QuotaBytes-acc.UsedBytes-acc.ReservedBytes)
		out.AvailableBytes = &avail
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
