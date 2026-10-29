// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	apispec "github.com/shukiv/selfcloud/api"
	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/fsutil"
	"github.com/shukiv/selfcloud/internal/version"
)

type readiness struct {
	Status string            `json:"status"` // "ready" or "not_ready"
	Checks map[string]string `json:"checks"` // "ok", "failed" or "unknown"
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}

// handleReadyz checks the database, that the data directory is writable, and free disk. The
// answer is unauthenticated, so failures are reported as "failed" and their causes only logged.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]string{}
	ready := true
	check := func(name string, err error) {
		if err != nil {
			s.log.Warn("readiness check failed", "check", name, "error", err)
			checks[name] = "failed"
			ready = false
			return
		}
		checks[name] = "ok"
	}

	check("database", s.store.Ping(ctx))
	check("data_dir", probeWritable(s.cfg.DataDir))

	free, err := fsutil.FreeBytes(s.cfg.DataDir)
	switch {
	case errors.Is(err, fsutil.ErrUnsupported):
		checks["disk_free"] = "unknown"
	case err == nil && free < uint64(s.cfg.Storage.MinFree):
		check("disk_free", errors.New("free disk is below storage.min_free"))
	default:
		check("disk_free", err)
	}

	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, readiness{Status: "not_ready", Checks: checks})
		return nil
	}
	writeJSON(w, http.StatusOK, readiness{Status: "ready", Checks: checks})
	return nil
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".readyz-*")
	if err != nil {
		return err
	}
	name := f.Name()
	err = f.Close()
	return errors.Join(err, os.Remove(name))
}

type infoResponse struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	PublicURL    string    `json:"public_url"`
	WebInterface bool      `json:"web_interface"`
	Brand        infoBrand `json:"brand"`
	// PasswordMinLength is the shortest password the server accepts. Clients check against it.
	PasswordMinLength int        `json:"password_min_length"`
	Limits            infoLimits `json:"limits"`
	Features          []string   `json:"features"`
}

type infoLimits struct {
	MaxFileSize  int64 `json:"max_file_size"`
	MaxChunkSize int64 `json:"max_chunk_size"`
}

// infoBrand is the installation's look. The logo is a URL, never the file's path on the host.
type infoBrand struct {
	Name    string  `json:"name"`
	Accent  string  `json:"accent"`
	LogoURL *string `json:"logo_url"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, infoResponse{
		Name:              "Selfcloud",
		Version:           version.Version,
		PublicURL:         s.cfg.PublicURL,
		WebInterface:      s.cfg.UI.Enabled,
		Brand:             s.brandInfo(),
		PasswordMinLength: auth.MinPasswordLen,
		Limits: infoLimits{
			MaxFileSize:  int64(s.cfg.Upload.MaxFileSize),
			MaxChunkSize: int64(s.cfg.Upload.MaxChunkSize),
		},
		Features: []string{},
	})
	return nil
}

// brandInfo describes the brand; the logo URL is set only when there is a logo.
func (s *Server) brandInfo() infoBrand {
	b := infoBrand{Name: s.cfg.UI.Brand.Name, Accent: s.cfg.UI.Brand.Accent}
	if s.logo != nil {
		u := logoPath
		b.LogoURL = &u
	}
	return b
}

// logoPath is the URL of the logo. It is served whether or not the web interface is.
const logoPath = "/brand/logo"

// handleLogo serves the installation's logo, or answers not_found when there is none.
func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) error {
	if s.logo == nil {
		return apierr.New(apierr.NotFound, "the installation has no logo")
	}
	s.logo.ServeHTTP(w, r)
	return nil
}

func (s *Server) handleSpec(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/yaml")
	_, err := w.Write(apispec.Spec)
	return err
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) error {
	s.metrics.handler().ServeHTTP(w, r)
	return nil
}
