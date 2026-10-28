package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"selfcloud/internal/apierr"
	"selfcloud/internal/id"
	"selfcloud/internal/store"
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

// handleCreateAccount creates an account. With an external_id, repeating the same request
// returns the existing account (200) instead of creating another, so retries are safe.
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	var in struct {
		Name       string `json:"name"`
		ExternalID string `json:"external_id"`
		QuotaBytes int64  `json:"quota_bytes"`
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

	ctx := r.Context()
	var out *store.Account
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
		return tx.InsertAudit(ctx, s.auditEntry(r, out.ID, "account.create", out.ID,
			map[string]any{"name": name, "external_id": ext, "quota_bytes": in.QuotaBytes}))
	})
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/accounts/"+out.ID)
	}
	writeJSON(w, status, toAccountJSON(out))
	return nil
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	page, err := pageParams(r)
	if err != nil {
		return err
	}
	accounts, next, err := s.store.Accounts(r.Context(), r.URL.Query().Get("external_id"), page)
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
func (s *Server) runAccountDelete(ctx context.Context, job *store.Job) error {
	return s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteAccount(ctx, job.Target); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, &store.AuditEntry{Time: store.Now(), AccountID: job.Target,
			ActorKind: "system", ActorID: job.ID, Action: "account.deleted", Target: job.Target})
	})
}

type usageJSON struct {
	QuotaBytes     int64  `json:"quota_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	UsedFiles      int64  `json:"used_files"`
	ReservedBytes  int64  `json:"reserved_bytes"`
	AvailableBytes *int64 `json:"available_bytes"` // null when the quota is unlimited
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) error {
	acc, err := s.managedAccount(r)
	if err != nil {
		return err
	}
	out := usageJSON{QuotaBytes: acc.QuotaBytes, UsedBytes: acc.UsedBytes, UsedFiles: acc.UsedFiles,
		ReservedBytes: acc.ReservedBytes}
	if acc.QuotaBytes > 0 {
		avail := max(0, acc.QuotaBytes-acc.UsedBytes-acc.ReservedBytes)
		out.AvailableBytes = &avail
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
