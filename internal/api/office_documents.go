// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// New documents (decision 91, extended by decision 105): POST /api/v1/nodes/{nod}/documents makes a file in the folder or
// directory {nod}, either blank ({"type": ...}) or a copy of a template ({"template": ...}). The rights, the quota, the
// disk check, the audit row and the webhooks are those of an upload of the same file: the caller needs editor on the
// directory, and the new node is written as handleCreateFile writes one, audited as node.create with via "document" or
// "template". A blank file's bytes go through the one-request write path (decision 84). A copy of an account template
// shares the template's bytes, as a file copy does (copyTree), so it writes no bytes and reserves no disk; its quota is
// charged in full, and its rate limit is the one-request limit.
//
// The types are the OOXML kinds (docx, xlsx, pptx), the OpenDocument kinds (odt, ods, odp, odg) and the empty text file
// (txt). Only txt is made with office editing off; every other type and every template is not found while it is off, as the
// other office routes are.
//
// The name is the default for the kind in the caller's language, or the name given, with the extension of the file added
// when the name does not end in it. A built-in template's default is its display name; an account template's is its own
// name. A name taken in the directory gets the first free "name (n).ext" (saveAsFreeName, decision 87).

import (
	"context"
	"net/http"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blankdoc"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/locale"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/templates"
)

// documentNames is the default name of each kind (without its extension), by language. Every language the server ships
// has all the kinds of nameKind; a language the server does not ship gets English (locale.Resolve).
var documentNames = map[string]map[string]string{
	"en": {"docx": "Untitled document", "xlsx": "Untitled spreadsheet", "pptx": "Untitled presentation",
		"odg": "Untitled drawing", "txt": "Untitled text file"},
	"es": {"docx": "Documento sin título", "xlsx": "Hoja de cálculo sin título", "pptx": "Presentación sin título",
		"odg": "Dibujo sin título", "txt": "Archivo de texto sin título"},
	"fr": {"docx": "Document sans titre", "xlsx": "Feuille de calcul sans titre", "pptx": "Présentation sans titre",
		"odg": "Dessin sans titre", "txt": "Fichier texte sans titre"},
	"pt": {"docx": "Documento sem título", "xlsx": "Planilha sem título", "pptx": "Apresentação sem título",
		"odg": "Desenho sem título", "txt": "Arquivo de texto sem título"},
	"ru": {"docx": "Документ без названия", "xlsx": "Таблица без названия", "pptx": "Презентация без названия",
		"odg": "Рисунок без названия", "txt": "Текстовый файл без названия"},
	// Machine drafts (decision 91): ar and he are to be checked by a native speaker.
	"ar": {"docx": "مستند بدون عنوان", "xlsx": "جدول بيانات بدون عنوان", "pptx": "عرض تقديمي بدون عنوان",
		"odg": "رسم بدون عنوان", "txt": "ملف نصي بدون عنوان"},
	"he": {"docx": "מסמך ללא שם", "xlsx": "גיליון ללא שם", "pptx": "מצגת ללא שם",
		"odg": "ציור ללא שם", "txt": "קובץ טקסט ללא שם"},
}

// nameKind is the key in documentNames of each type: an OpenDocument kind takes the name of the OOXML kind it stands for.
var nameKind = map[string]string{"docx": "docx", "odt": "docx", "xlsx": "xlsx", "ods": "xlsx", "pptx": "pptx", "odp": "pptx",
	"odg": "odg", "txt": "txt"}

// documentLanguage is the language of the default name: the signed-in person's stored locale, or English for a key.
func (s *Server) documentLanguage(ctx context.Context, p *auth.Principal, accountID string) string {
	if p == nil || p.UserID == "" {
		return locale.Default
	}
	u, err := s.store.User(ctx, accountID, p.UserID)
	if err != nil {
		return locale.Default
	}
	return locale.Resolve(u.Locale)
}

// fileName is the name a new file is asked for: the given name, or base, with the extension ext added when the name does
// not already end in it (in any case). A given name is checked as a name first, so "." or a name with a slash is refused,
// not turned into "..docx".
func fileName(base, ext, given string) (string, string, error) {
	suffix := "." + ext
	name := given
	if name == "" {
		name = base
	}
	if given != "" {
		if _, _, err := names.Clean(given); err != nil {
			return "", "", err
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), suffix) {
		name += suffix
	}
	return names.Clean(name)
}

// documentName is fileName for a blank file of a type, with the default name of the type in lang.
func documentName(lang, kind, given string) (string, string, error) {
	return fileName(documentNames[lang][nameKind[kind]], kind, given)
}

// storeBytes writes body as a new blob of accountID. It takes the same rate limit, disk reservation and quota check as a
// one-request write (writeBlob, decision 84), before the bytes are written, and removes the blob on a failed write.
func (s *Server) storeBytes(ctx context.Context, p *auth.Principal, accountID string, body []byte) (blob.Committed, error) {
	size := int64(len(body))
	if p != nil {
		if err := s.rateLimit(oneRequestWriteRate, string(p.Kind)+":"+p.ID); err != nil {
			return blob.Committed{}, err
		}
	}
	release, err := s.reserveOneRequest(ctx, p, accountID, size, 0, size)
	if err != nil {
		return blob.Committed{}, err
	}
	defer release()
	wr, err := s.blobs.Create(ctx, accountID, size)
	if err != nil {
		return blob.Committed{}, err
	}
	defer wr.Abort() //nolint:errcheck // removes the partial file unless Commit succeeded
	if _, err := wr.Write(body); err != nil {
		return blob.Committed{}, err
	}
	return wr.Commit()
}

// handleCreateDocument makes a file in the directory {nod}: a blank one of a type, or a copy of a template. It answers 201
// with the new node.
func (s *Server) handleCreateDocument(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Template string `json:"template"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	if (in.Type == "") == (in.Template == "") {
		return apierr.New(apierr.InvalidRequest, "give a type or a template, not both")
	}
	if in.Type != "" && blankdoc.MIME(in.Type) == "" {
		return apierr.New(apierr.InvalidRequest, "type must be docx, xlsx, pptx, odt, ods, odp, odg or txt")
	}
	// Only the empty text file is made with office editing off; a template is an office file, so it is not found either.
	if s.office == nil && in.Type != "txt" {
		return notFound("node")
	}
	ctx := r.Context()
	p := principalOf(r)
	dir, err := s.nodeOf(r, r.PathValue("nod"), store.RoleEditor)
	if err != nil {
		return err
	}
	if dir.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "a document goes in a folder")
	}
	lang := s.documentLanguage(ctx, p, dir.AccountID)
	var out *store.Node
	if in.Type != "" {
		out, err = s.makeBlankDocument(r, p, dir, lang, in.Type, in.Name)
	} else {
		out, err = s.makeFromTemplate(r, p, dir, lang, in.Template, in.Name)
	}
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	writeJSON(w, http.StatusCreated, toNodeJSON(out))
	return nil
}

// makeBlankDocument makes a blank file of a type in dir, audited as via "document".
func (s *Server) makeBlankDocument(r *http.Request, p *auth.Principal, dir *store.Node, lang, kind, given string) (*store.Node, error) {
	name, key, err := documentName(lang, kind, given)
	if err != nil {
		return nil, err
	}
	body, err := blankdoc.Build(kind)
	if err != nil {
		return nil, apierr.Wrap(apierr.Internal, "the blank document could not be made", err)
	}
	c, err := s.storeBytes(r.Context(), p, dir.AccountID, body)
	if err != nil {
		return nil, err
	}
	return s.placeDocument(r, p, dir, name, key, blankdoc.MIME(kind), c, true,
		map[string]any{"via": "document"}, nil)
}

// makeFromTemplate copies a template into dir. A built-in template is built and stored as a blank file is. An account
// template is a file of the account the caller reads (a person who cannot read it is told it is not found), and its copy
// shares its bytes.
func (s *Server) makeFromTemplate(r *http.Request, p *auth.Principal, dir *store.Node, lang, ref, given string) (*store.Node, error) {
	ctx := r.Context()
	acc := dir.AccountID
	if strings.HasPrefix(ref, "builtin:") {
		tpl, ok := templates.Get(ref)
		if !ok {
			return nil, apierr.New(apierr.InvalidRequest, "there is no built-in template "+ref)
		}
		name, key, err := fileName(tpl.Name(lang), tpl.Ext, given)
		if err != nil {
			return nil, err
		}
		body, err := tpl.Build()
		if err != nil {
			return nil, apierr.Wrap(apierr.Internal, "the template could not be made", err)
		}
		c, err := s.storeBytes(ctx, p, acc, body)
		if err != nil {
			return nil, err
		}
		return s.placeDocument(r, p, dir, name, key, blankdoc.MIME(tpl.Ext), c, true,
			map[string]any{"via": "template", "template": tpl.ID}, nil)
	}
	if !id.Valid("nod", ref) {
		return nil, apierr.New(apierr.InvalidRequest, "template must be a built-in template or a file's node ID")
	}
	src, _, err := s.nodeAccess(r, ref)
	if err != nil {
		return nil, err
	}
	if src.AccountID != acc {
		return nil, nodeNotFound()
	}
	if src.Kind != store.KindFile {
		return nil, apierr.New(apierr.InvalidRequest, "a template is a file")
	}
	ext := templateExtOf(src.Name)
	if ext == "" {
		return nil, apierr.New(apierr.InvalidRequest, "that file is not a template: its extension is not a template's")
	}
	name, key, err := fileName(src.Name, ext, given)
	if err != nil {
		return nil, err
	}
	if p != nil {
		if err := s.rateLimit(oneRequestWriteRate, string(p.Kind)+":"+p.ID); err != nil {
			return nil, err
		}
	}
	c := blob.Committed{ID: src.BlobID, Size: src.Size, SHA256: src.SHA256}
	return s.placeDocument(r, p, dir, name, key, blankdoc.MIME(ext), c, false,
		map[string]any{"via": "template", "template": src.ID}, src)
}

// placeDocument writes a new file node named name, of media type mt, into dir, with the content c. A fresh blob is
// inserted with the node; a shared one (fresh false) already exists. The quota is checked and charged in full, and the
// row is audited as node.create with details added to kind, name, parent and size. A failed write removes a fresh blob.
func (s *Server) placeDocument(r *http.Request, p *auth.Principal, dir *store.Node, name, key, mt string,
	c blob.Committed, fresh bool, details map[string]any, tmpl *store.Node) (*store.Node, error) {
	ctx := r.Context()
	acc := dir.AccountID
	var out *store.Node
	s.atWrite()
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		parent, err := requireNodeRole(ctx, tx, p, dir.ID, store.RoleEditor)
		if err != nil {
			return err
		}
		if parent.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "a document goes in a folder")
		}
		// A copy of an account template is made as a copy of a file is (copy route, decision 87): the no-download rule
		// is read from this transaction, on the template's state and the caller's role on it. A viewer or commenter is
		// refused, and a copy of a contributor or above keeps the template's own flag (copyTree keeps it too).
		keepNoDownload := false
		if tmpl != nil {
			cur, tRole, err := nodeAccessWith(ctx, tx, p, tmpl.ID)
			if err != nil {
				return err
			}
			restricted, err := s.copyRestricted(ctx, tx, cur, tRole)
			if err != nil {
				return err
			}
			if restricted {
				return errDownloadRestricted()
			}
			keepNoDownload = cur.NoDownload
		}
		finalName, finalKey := name, key
		taken, err := nameTakenBy(tx.Child(ctx, acc, parent.ID, key))
		if err != nil {
			return err
		}
		if taken != nil {
			if finalName, finalKey, err = saveAsFreeName(ctx, tx, acc, parent.ID, name); err != nil {
				return err
			}
		}
		if fresh {
			if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: acc, Size: c.Size,
				SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
				return err
			}
		}
		n, err := s.newNode(ctx, tx, acc, parent.FolderID, parent.ID, pathSeg{name: finalName, key: finalKey}, store.KindFile, p.ID)
		if err != nil {
			return err
		}
		n.Size, n.MimeType, n.BlobID, n.ETag, n.SHA256 = c.Size, mt, c.ID, newETag(), c.SHA256
		n.ContentTag = store.NewContentTag()
		n.NoDownload = keepNoDownload
		if err := tx.UpdateNode(ctx, n); err != nil {
			return err
		}
		if err := s.applyUsage(ctx, tx, p, acc, store.FolderChange{FolderID: parent.FolderID,
			Change: store.UsageChange{Bytes: c.Size, Files: 1}}); err != nil {
			return err
		}
		out = n
		if err := noteEdit(ctx, tx, p, acc, n.ID); err != nil {
			return err
		}
		audit := map[string]any{"kind": store.KindFile, "name": n.Name, "parent": parent.ID, "size": c.Size}
		for k, v := range details {
			audit[k] = v
		}
		return tx.InsertAudit(ctx, s.auditEntry(r, acc, "node.create", n.ID, audit, parent.FolderID))
	})
	if err != nil {
		if fresh {
			s.discardBlob(acc, c.ID)
		}
		return nil, err
	}
	return out, nil
}
