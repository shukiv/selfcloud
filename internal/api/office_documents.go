// SPDX-License-Identifier: AGPL-3.0-or-later

package api

// New documents (decision 91): POST /api/v1/nodes/{nod}/documents makes a blank Word document, workbook or presentation in
// the folder or directory {nod}, and the web interface opens it in the editor (decision 72). The rights, the quota, the disk
// check, the audit row and the webhooks are those of an upload of the same file: the caller needs contributor on the
// directory, the bytes are reserved and checked as a one-request write is (decision 84), and the new node is written as
// handleCreateFile writes one, audited as node.create with via "document".
//
// The name is the default for the type in the caller's language, or the name given, with the type's extension added when
// it does not end in it. A name taken in the directory gets the first free "name (n).ext", the rule saveAsFreeName reads
// from freeName (decision 87). The route exists only while office editing is on, as the other office routes do.

import (
	"context"
	"net/http"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/blankdoc"
	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/locale"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/store"
)

// documentNames is the default name of each kind (without its extension), by language. Every language the server ships
// has all three; a language the server does not ship gets English (locale.Resolve).
var documentNames = map[string]map[string]string{
	"en": {"docx": "Untitled document", "xlsx": "Untitled spreadsheet", "pptx": "Untitled presentation"},
	"es": {"docx": "Documento sin título", "xlsx": "Hoja de cálculo sin título", "pptx": "Presentación sin título"},
	"fr": {"docx": "Document sans titre", "xlsx": "Feuille de calcul sans titre", "pptx": "Présentation sans titre"},
	"pt": {"docx": "Documento sem título", "xlsx": "Planilha sem título", "pptx": "Apresentação sem título"},
	"ru": {"docx": "Документ без названия", "xlsx": "Таблица без названия", "pptx": "Презентация без названия"},
	// Machine drafts (decision 91): ar and he are to be checked by a native speaker.
	"ar": {"docx": "مستند بدون عنوان", "xlsx": "جدول بيانات بدون عنوان", "pptx": "عرض تقديمي بدون عنوان"},
	"he": {"docx": "מסמך ללא שם", "xlsx": "גיליון ללא שם", "pptx": "מצגת ללא שם"},
}

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

// documentName is the name a new document is asked for: the given name, or the default in lang, with the kind's extension.
// A given name that already ends in the extension (in any case) is kept; another name gets it added.
func documentName(lang, kind, given string) (string, string, error) {
	ext := "." + kind
	name := given
	if name == "" {
		name = documentNames[lang][kind]
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return names.Clean(name)
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

// handleCreateDocument makes a blank document in the directory {nod}. It answers 201 with the new node.
func (s *Server) handleCreateDocument(w http.ResponseWriter, r *http.Request) error {
	if s.office == nil {
		return notFound("node")
	}
	var in struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		return err
	}
	mt := blankdoc.MIME(in.Type)
	if mt == "" {
		return apierr.New(apierr.InvalidRequest, "type must be docx, xlsx or pptx")
	}
	ctx := r.Context()
	p := principalOf(r)
	dir, err := s.nodeOf(r, r.PathValue("nod"), store.RoleContributor)
	if err != nil {
		return err
	}
	if dir.Kind != store.KindDir {
		return apierr.New(apierr.InvalidRequest, "a document goes in a folder")
	}
	name, key, err := documentName(s.documentLanguage(ctx, p, dir.AccountID), in.Type, in.Name)
	if err != nil {
		return err
	}
	body, err := blankdoc.Build(in.Type)
	if err != nil {
		return apierr.Wrap(apierr.Internal, "the blank document could not be made", err)
	}
	acc := dir.AccountID
	c, err := s.storeBytes(ctx, p, acc, body)
	if err != nil {
		return err
	}
	var out *store.Node
	s.atWrite()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		parent, err := requireNodeRole(ctx, tx, p, dir.ID, store.RoleContributor)
		if err != nil {
			return err
		}
		if parent.Kind != store.KindDir {
			return apierr.New(apierr.InvalidRequest, "a document goes in a folder")
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
		if err := tx.InsertBlob(ctx, &store.Blob{ID: c.ID, AccountID: acc, Size: c.Size,
			SHA256: c.SHA256, State: store.BlobLive, CreatedAt: store.Now()}); err != nil {
			return err
		}
		n, err := s.newNode(ctx, tx, acc, parent.FolderID, parent.ID, pathSeg{name: finalName, key: finalKey}, store.KindFile, p.ID)
		if err != nil {
			return err
		}
		n.Size, n.MimeType, n.BlobID, n.ETag, n.SHA256 = c.Size, mt, c.ID, newETag(), c.SHA256
		n.ContentTag = store.NewContentTag()
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
		return tx.InsertAudit(ctx, s.auditEntry(r, acc, "node.create", n.ID,
			map[string]any{"kind": store.KindFile, "name": n.Name, "parent": parent.ID, "size": c.Size, "via": "document"},
			parent.FolderID))
	})
	if err != nil {
		s.discardBlob(acc, c.ID)
		return err
	}
	w.Header().Set("Location", "/api/v1/nodes/"+out.ID)
	writeJSON(w, http.StatusCreated, toNodeJSON(out))
	return nil
}
