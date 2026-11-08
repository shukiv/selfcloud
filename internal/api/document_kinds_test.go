// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import "testing"

// Decision 105: New makes the OpenDocument kinds with office editing on. Each has the default name of the kind it stands
// for, its media type, and a package whose mimetype entry is first and stored, at byte 38.
func TestNewDocumentMakesTheOpenDocumentKinds(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	want := map[string]struct{ name, mime string }{
		"odt": {"Untitled document.odt", "application/vnd.oasis.opendocument.text"},
		"ods": {"Untitled spreadsheet.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		"odp": {"Untitled presentation.odp", "application/vnd.oasis.opendocument.presentation"},
		"odg": {"Untitled drawing.odg", "application/vnd.oasis.opendocument.graphics"},
	}
	for kind, w := range want {
		n := nodeOf(t, newDocumentCall(t, fx.s, fx.key, fx.fld.RootNodeID, map[string]any{"type": kind}))
		if n.Name != w.name {
			t.Errorf("%s: Name = %q, want %q", kind, n.Name, w.name)
		}
		if n.MimeType != w.mime {
			t.Errorf("%s: MimeType = %q, want %q", kind, n.MimeType, w.mime)
		}
		body := call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID+"/content", fx.key, nil).Body.Bytes()
		if len(body) < 38+len(w.mime) || string(body[38:38+len(w.mime)]) != w.mime {
			t.Errorf("%s: the served file does not carry its media type at byte 38", kind)
		}
	}
}

// The text file is an empty plain file, made with office editing off: it needs no editor. The office kinds stay not
// found while office editing is off, the OpenDocument ones too.
func TestNewTextFileIsMadeWithOfficeOff(t *testing.T) {
	t.Parallel()
	s, _, key, _, f := quotaSetup(t, 0)
	n := nodeOf(t, newDocumentCall(t, s, key, f.RootNodeID, map[string]any{"type": "txt"}))
	if n.Name != "Untitled text file.txt" {
		t.Errorf("Name = %q, want Untitled text file.txt", n.Name)
	}
	if n.Size != 0 || n.MimeType != "text/plain" {
		t.Errorf("Size = %d, MimeType = %q; want an empty text/plain file", n.Size, n.MimeType)
	}
	for _, kind := range []string{"docx", "odt", "odg"} {
		wantStatus(t, newDocumentCall(t, s, key, f.RootNodeID, map[string]any{"type": kind}), 404)
	}
}

// The text file and the drawing have their names in the signed-in person's language.
func TestNewDocumentNamesOfTheNewKindsFollowTheLanguage(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	fx.grant(t, fx.fld.RootNodeID, fx.ann.ID, "editor")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/me", map[string]any{"locale": "he"}), 200)
	if got := nodeOf(t, newDocumentAs(t, fx.s, fx.annSess, fx.fld.RootNodeID, map[string]any{"type": "txt"})); got.Name != "קובץ טקסט ללא שם.txt" {
		t.Errorf("Hebrew text file Name = %q", got.Name)
	}
	if got := nodeOf(t, newDocumentAs(t, fx.s, fx.annSess, fx.fld.RootNodeID, map[string]any{"type": "odg"})); got.Name != "ציור ללא שם.odg" {
		t.Errorf("Hebrew drawing Name = %q", got.Name)
	}
}
