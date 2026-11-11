// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/web"
)

// The brand while the server runs (decision 141). The brand keys are settings, changed through PATCH /api/v1/settings. An
// installation admin uploads or removes the logo and the icon through PUT and DELETE /api/v1/settings/brand/logo and
// /brand/icon. A picture is checked as the config file's pictures are, stored under <data_dir>/brand with a name the server
// makes from its hash, and recorded in the settings table with its type and hash, in the transaction of its audit entry.
// After each change the web interface's brand is built whole, before anything is stored, and swapped in once it is.

// The states of a picture in the settings answer.
const (
	pictureNone     = "none"
	pictureFile     = "file"
	pictureUploaded = "uploaded"
)

// pictureInvalid is the audit's old value for a stored row the server did not write. Its text is never shown.
const pictureInvalid = "invalid"

// picture is one of the installation's pictures as the server serves it: its bytes, or nil for none, and where it comes
// from. ignored is the stored row the start did not use because it or its file failed the checks; DELETE or an upload
// clears it.
type picture struct {
	img     *web.Logo
	from    string
	ignored string
}

// brandPictures are the installation's logo and icon as the server serves them now. It is never changed: a change swaps in
// a new one.
type brandPictures struct {
	logo, icon picture
}

// pictureKind is one of the two pictures: the settings table's row, the stem of its file's name, and its check.
type pictureKind struct {
	key   string
	stem  string
	check func(data []byte, contentType string) (*web.Logo, error)
}

var (
	logoKind = pictureKind{key: config.LogoPicture, stem: "logo", check: web.NewLogo}
	iconKind = pictureKind{key: config.IconPicture, stem: "icon", check: web.NewIcon}
)

// of returns the kind's picture in p.
func (k pictureKind) of(p *brandPictures) picture {
	if k.stem == logoKind.stem {
		return p.logo
	}
	return p.icon
}

// with returns p with the kind's picture replaced by pic.
func (k pictureKind) with(p *brandPictures, pic picture) *brandPictures {
	next := *p
	if k.stem == logoKind.stem {
		next.logo = pic
	} else {
		next.icon = pic
	}
	return &next
}

// fileName is the name the server gives the kind's picture of type ct and hash sum: the stem, the hash and the type's
// extension. Nothing a client sends is in it but through the hash the server computed.
func (k pictureKind) fileName(ct, sum string) string {
	ext := ".png"
	if ct == web.TypeSVG {
		ext = ".svg"
	}
	return k.stem + "-" + sum + ext
}

// pictureRowPattern is the settings table's text for an uploaded picture: its type and the hex SHA-256 of its bytes.
var pictureRowPattern = regexp.MustCompile(`^(image/png|image/svg\+xml) sha256:([0-9a-f]{64})$`)

// pictureText is a picture as the settings table and the audit hold it: its type and hash, or none.
func pictureText(p picture) string {
	if p.img == nil {
		return pictureNone
	}
	return p.img.ContentType() + " sha256:" + p.img.SHA256()
}

// storedText is what the settings table holds for p, as the audit shows it: the picture's type and hash, the row the start
// ignored, or none. A row the server did not write is shown as invalid.
func storedText(p picture) string {
	if p.img != nil || p.ignored == "" {
		return pictureText(p)
	}
	if !pictureRowPattern.MatchString(p.ignored) {
		return pictureInvalid
	}
	return p.ignored
}

// uploadedFile is the name of p's file in the brand folder: the upload in use, or the one a row the start ignored names
// when the server wrote that row. It is empty for none, for the config file's picture, and for a row the server did not
// write, so no path is ever made from such a row.
func (k pictureKind) uploadedFile(p picture) string {
	if p.from == pictureUploaded {
		return k.fileName(p.img.ContentType(), p.img.SHA256())
	}
	if m := pictureRowPattern.FindStringSubmatch(p.ignored); m != nil {
		return k.fileName(m[1], m[2])
	}
	return ""
}

// brandDir is the folder of the uploaded pictures.
func (s *Server) brandDir() string {
	return filepath.Join(s.cfg.DataDir, "brand")
}

// startPictures reads the installation's pictures at start. A picture the config file sets is read as it always was, and a
// file that fails stops the start. Otherwise the upload stored in rows is read from the brand folder and checked again; one
// that no longer passes is logged and not used, and never stops the start. An upload under a picture the file locks is kept,
// unused, and comes back when the file stops setting it.
func (s *Server) startPictures(cfg *config.Config, rows map[string]string) (*brandPictures, error) {
	p := &brandPictures{logo: picture{from: pictureNone}, icon: picture{from: pictureNone}}
	for _, k := range []pictureKind{logoKind, iconKind} {
		file, load := cfg.UI.Brand.LogoFile, web.LoadLogo
		if k.stem == iconKind.stem {
			file, load = cfg.UI.Brand.IconFile, web.LoadIconFile
		}
		row, stored := rows[k.key]
		if cfg.PictureLocked(k.key) {
			if file != "" {
				img, err := load(file)
				if err != nil {
					return nil, fmt.Errorf("api: %w", err)
				}
				if w := img.SizeWarning(); w != "" && k.stem == logoKind.stem {
					s.log.Warn(w)
				}
				p = k.with(p, picture{img: img, from: pictureFile})
			}
			if stored {
				s.log.Warn("an uploaded picture is not used: the config file or the environment sets its file, and wins", "key", k.key)
			}
			continue
		}
		if !stored {
			continue
		}
		img, err := s.readStoredPicture(k, row)
		if err != nil {
			s.log.Warn("an uploaded picture is not used", "key", k.key, "reason", err.Error())
			p = k.with(p, picture{from: pictureNone, ignored: row})
			continue
		}
		p = k.with(p, picture{img: img, from: pictureUploaded})
	}
	return p, nil
}

// readStoredPicture reads the upload a settings row names from the brand folder, and checks it as an upload is checked: its
// file must be a regular file, its bytes must pass, and their hash must be the row's.
func (s *Server) readStoredPicture(k pictureKind, row string) (*web.Logo, error) {
	m := pictureRowPattern.FindStringSubmatch(row)
	if m == nil {
		return nil, errors.New("the stored row is not a picture the server wrote")
	}
	ct, sum := m[1], m[2]
	path := filepath.Join(s.brandDir(), k.fileName(ct, sum))
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := web.ReadPicture(path)
	if err != nil {
		return nil, err
	}
	img, err := k.check(data, ct)
	if err != nil {
		return nil, err
	}
	if img.SHA256() != sum {
		return nil, fmt.Errorf("%s does not have the hash its row stores", path)
	}
	return img, nil
}

// brandOptions are the web interface's brand for the settings c and the pictures p. The SelfCloud artwork shows when there
// is no logo at all, from the file or an upload (decision 126).
func brandOptions(c *config.Config, p *brandPictures) web.BrandOptions {
	return web.BrandOptions{Name: c.UI.Brand.Name, Accent: c.UI.Brand.Accent, Artwork: p.logo.img == nil,
		Icon: p.icon.img, Logo: p.logo.img}
}

// prepareBrand builds the web interface's brand for the settings c and the pictures p, without serving it. With the
// interface off there is nothing to build. A brand that cannot be built is an error, and the brand in use stays.
func (s *Server) prepareBrand(c *config.Config, p *brandPictures) (*web.Brand, error) {
	if s.buildBrand == nil {
		return nil, nil
	}
	b, err := s.buildBrand(brandOptions(c, p))
	if err != nil {
		return nil, fmt.Errorf("api: the brand cannot be built: %w", err)
	}
	return b, nil
}

// useBrand swaps in the pictures and the brand prepareBrand built, once the change is stored, and logs the brand's notes and
// the colours that fall short of the contrast the text needs, as a start does.
func (s *Server) useBrand(c *config.Config, p *brandPictures, b *web.Brand) {
	s.pics.Store(p)
	if b != nil {
		s.ui.UseBrand(b)
		for _, note := range b.Notes() {
			s.log.Info(note)
		}
	}
	for _, w := range c.UI.Brand.ContrastWarnings() {
		s.log.Warn("brand colours fall short of the contrast the text needs; the config is kept", "detail", w)
	}
}

// pictureJSON is one picture in the settings answer: none, the config file's, or an upload, with its type and hash. Its path
// is never shown.
type pictureJSON struct {
	Key    string `json:"key"`
	State  string `json:"state"`
	Type   string `json:"type,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Locked bool   `json:"locked"`
}

// picturesOf lists the pictures, sorted by key.
func picturesOf(c *config.Config, p *brandPictures) []pictureJSON {
	out := make([]pictureJSON, 0, 2)
	for _, k := range []pictureKind{iconKind, logoKind} {
		pic := k.of(p)
		j := pictureJSON{Key: k.key, State: pic.from, Locked: c.PictureLocked(k.key)}
		if pic.img != nil {
			j.Type, j.SHA256 = pic.img.ContentType(), pic.img.SHA256()
		}
		out = append(out, j)
	}
	return out
}

func (s *Server) handlePutLogo(w http.ResponseWriter, r *http.Request) error {
	return s.putPicture(w, r, logoKind)
}

func (s *Server) handlePutIcon(w http.ResponseWriter, r *http.Request) error {
	return s.putPicture(w, r, iconKind)
}

func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) error {
	return s.deletePicture(w, r, logoKind)
}

func (s *Server) handleDeleteIcon(w http.ResponseWriter, r *http.Request) error {
	return s.deletePicture(w, r, iconKind)
}

// pictureLocked refuses a change to a picture the config file or the environment sets.
func pictureLocked(k pictureKind) error {
	return apierr.New(apierr.Conflict, "set in the server's config file or environment: "+k.key+"_file").
		With("reason", "setting_locked").With("keys", []string{k.key})
}

// pictureType reads the declared type of an upload: image/png or image/svg+xml, with any parameters.
func pictureType(r *http.Request) (string, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mt != web.TypePNG && mt != web.TypeSVG) {
		return "", apierr.New(apierr.UnsupportedMedia, "a picture is sent as image/png or image/svg+xml")
	}
	return mt, nil
}

// pictureTooLarge refuses a body over the picture cap.
func pictureTooLarge() error {
	return apierr.Newf(apierr.TooLarge, "a picture is at most %d bytes", web.MaxLogoBytes).With("limit_bytes", web.MaxLogoBytes)
}

// putPicture stores an upload of the kind k: the admin and the lock first, then the declared type and the size, then the
// bytes through the check. The body is read through a cap, so a larger one is refused before it is read in full; one that
// declares a larger length is refused unread. The same picture again changes nothing.
func (s *Server) putPicture(w http.ResponseWriter, r *http.Request, k pictureKind) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	if s.settings().PictureLocked(k.key) {
		return pictureLocked(k)
	}
	ct, err := pictureType(r)
	if err != nil {
		return err
	}
	if r.ContentLength > web.MaxLogoBytes {
		return pictureTooLarge()
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, web.MaxLogoBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return pictureTooLarge()
		}
		return err
	}
	img, err := k.check(data, ct)
	if err != nil {
		return apierr.New(apierr.InvalidRequest, err.Error()).With("reason", "invalid_picture")
	}

	// One change at a time, the settings' and the pictures' alike: each builds on the brand the one before it swapped in.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cur := s.pics.Load()
	old := k.of(cur)
	if old.from == pictureUploaded && old.img.SHA256() == img.SHA256() && old.img.ContentType() == ct {
		writeJSON(w, http.StatusOK, s.settingsAnswer())
		return nil
	}
	name := k.fileName(ct, img.SHA256())
	existed, err := s.writePicture(name, data)
	if err != nil {
		return err
	}
	undo := func() {
		if !existed {
			s.removePicture(name)
		}
	}
	next := k.with(cur, picture{img: img, from: pictureUploaded})
	set := s.settings()
	built, err := s.prepareBrand(set, next)
	if err != nil {
		undo()
		return err
	}
	ctx := r.Context()
	changes := map[string]any{k.key: map[string]any{"old": storedText(old), "new": pictureText(k.of(next)), "source": string(config.SourceSettings)}}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(ctx, k.key, pictureText(k.of(next))); err != nil {
			return err
		}
		// The entry belongs to the installation (no account). The action is a literal, as the audit guards read it.
		return tx.InsertAudit(ctx, s.auditEntry(r, "", "installation.settings", "settings", changes))
	})
	if err != nil {
		undo()
		return err
	}
	s.useBrand(set, next, built)
	if oldName := k.uploadedFile(old); oldName != "" && oldName != name {
		s.removePicture(oldName)
	}
	writeJSON(w, http.StatusOK, s.settingsAnswer())
	return nil
}

// deletePicture removes the upload of the kind k: the picture goes back to the SelfCloud artwork (decision 126), or for the
// icon to what decision 119 picks without it. A stored row the start ignored is removed too, with the file it names, so the
// start stops warning about it. With neither it changes nothing.
func (s *Server) deletePicture(w http.ResponseWriter, r *http.Request, k pictureKind) error {
	if err := requireAdmin(r); err != nil {
		return err
	}
	if s.settings().PictureLocked(k.key) {
		return pictureLocked(k)
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cur := s.pics.Load()
	old := k.of(cur)
	if old.from != pictureUploaded && old.ignored == "" {
		writeJSON(w, http.StatusOK, s.settingsAnswer())
		return nil
	}
	next := k.with(cur, picture{from: pictureNone})
	set := s.settings()
	built, err := s.prepareBrand(set, next)
	if err != nil {
		return err
	}
	ctx := r.Context()
	changes := map[string]any{k.key: map[string]any{"old": storedText(old), "new": pictureNone, "source": string(config.SourceDefault)}}
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteSetting(ctx, k.key); err != nil {
			return err
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, "", "installation.settings", "settings", changes))
	})
	if err != nil {
		return err
	}
	s.useBrand(set, next, built)
	if name := k.uploadedFile(old); name != "" {
		s.removePicture(name)
	}
	writeJSON(w, http.StatusOK, s.settingsAnswer())
	return nil
}

// writePicture writes data to the brand folder under name: to a temporary file of mode 0600, synced, then renamed into place
// and the folder synced, so a crash leaves the old file or the new one whole. The folder is made with mode 0700. existed
// reports that a file of that name was there before.
func (s *Server) writePicture(name string, data []byte) (existed bool, err error) {
	dir := s.brandDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("api: brand folder: %w", err)
	}
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err == nil {
		existed = true
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return existed, fmt.Errorf("api: brand folder: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return existed, fmt.Errorf("api: brand picture: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return existed, fmt.Errorf("api: brand picture: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return existed, fmt.Errorf("api: brand picture: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return existed, fmt.Errorf("api: brand picture: %w", err)
	}
	keep = true
	if err := syncDir(dir); err != nil {
		return existed, fmt.Errorf("api: brand folder: %w", err)
	}
	return existed, nil
}

// removePicture removes a picture the brand no longer uses. A failure is logged: the file is left, and nothing reads it.
func (s *Server) removePicture(name string) {
	if err := os.Remove(filepath.Join(s.brandDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("a brand picture no longer used cannot be removed", "file", name, "error", err)
	}
}

// syncDir makes a rename in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	return errors.Join(err, d.Close())
}

// brandChanged reports whether a settings change touches the brand, so the web interface's brand is built again.
func brandChanged(changes map[string]any) bool {
	for k := range changes {
		if strings.HasPrefix(k, "ui.brand.") {
			return true
		}
	}
	return false
}

// settingsAnswer is the answer of every settings route: the settings, the facts and the pictures, as the server runs now.
func (s *Server) settingsAnswer() settingsJSON {
	set := s.settings()
	out := settingsOf(set)
	out.Pictures = picturesOf(set, s.pics.Load())
	return out
}
