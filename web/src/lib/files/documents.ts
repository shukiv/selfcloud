// SPDX-License-Identifier: AGPL-3.0-or-later

// New documents (decision 91, round 2 R5, decision 102). The New menu's Document, Spreadsheet and Presentation: the click
// opens the wait page in a new tab with a random key, and the opener makes the blank file and hands it over under that key
// (newDocumentHandoff.ts). A GET never creates a file: the wait page and the editor's address make none. On a coarse pointer
// the same wait page opens in this tab.
import { useCallback } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useLang } from '../i18n';
import { get } from '../store';
import { createDocument } from './api';
import { coarsePointer, safeFrom } from './editor';
import { fileErrorText } from './fileErrors';
import { finishHandoff, newKey, startHandoff, waitPath } from './newDocumentHandoff';
import { newPlace, placeForRoute } from './newPlace';

export type DocumentKind = 'docx' | 'xlsx' | 'pptx';

// openNewDocumentTab starts a new document: the key, the wait page (in a new tab with noopener from the click, or in this tab
// on a coarse pointer), and the one server call. The call's result, or its error text, is handed to the wait page.
export function openNewDocumentTab(
  o: {
    parent: string;
    kind: DocumentKind;
    from: string;
    coarse: boolean;
    goTo: (to: string) => void;
    fail: (err: unknown) => string;
  },
  open: (url: string, target: string, features: string) => unknown = (u, t, f) => window.open(u, t, f),
): void {
  const k = newKey();
  startHandoff(k);
  const back = safeFrom(o.from);
  if (o.coarse) o.goTo(waitPath(k));
  else open(waitPath(k), '_blank', 'noopener');
  const tab = !o.coarse;
  void createDocument(o.parent, o.kind).then(
    (node) => finishHandoff(k, { node: node.id, from: back, tab }),
    (err: unknown) => finishHandoff(k, { error: o.fail(err) }),
  );
}

// useNewDocument is the action the New menus call: it starts a document of kind in the place the New actions act on (the
// place the files page shows, when the page is the one the place is for: placeForRoute). Both the sidebar's New and the
// toolbar's New use it.
export function useNewDocument(): (kind: DocumentKind) => void {
  const navigate = useNavigate();
  const location = useLocation();
  const lang = useLang();
  return useCallback(
    (kind: DocumentKind) => {
      const place = placeForRoute(get(newPlace), location.pathname);
      if (!place?.nodeID) return;
      openNewDocumentTab({
        parent: place.nodeID,
        kind,
        from: location.pathname + location.search,
        coarse: coarsePointer(),
        goTo: (to) => navigate(to),
        fail: (err) => fileErrorText(err, lang),
      });
    },
    [navigate, location.pathname, location.search, lang],
  );
}
