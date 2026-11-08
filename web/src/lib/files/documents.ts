// SPDX-License-Identifier: AGPL-3.0-or-later

// New documents (decision 91, round 2 R5, decision 102). The New menu's Document, Spreadsheet and Presentation: the click
// opens the wait page in a new tab with a random key, and the opener makes the blank file and hands it over under that key
// (newDocumentHandoff.ts). A GET never creates a file: the wait page and the editor's address make none. On a coarse pointer
// the same wait page opens in this tab.
import { useCallback } from 'react';
import { App } from 'antd';
import { useQueryClient } from '@tanstack/react-query';
import { useLocation, useNavigate } from 'react-router-dom';
import { useLang, useT } from '../i18n';
import { get, useStore } from '../store';
import { session } from '../session';
import { createDocument } from './api';
import { coarsePointer, safeFrom } from './editor';
import { fileErrorText } from './fileErrors';
import { finishHandoff, newKey, startHandoff, waitPath } from './newDocumentHandoff';
import { refreshTree } from '../query';
import { newPlace, placeForRoute } from './newPlace';

export type DocumentKind = 'docx' | 'xlsx' | 'pptx';

// openNewDocumentTab starts a new document: the key, the wait page (in a new tab from the click, or in this tab on a coarse
// pointer), and the one server call. The call's result, or its error text, is handed to the wait page. When the browser
// blocks the new tab, nothing is made and blocked() says so (review, round 2: no file without a tab to open it in).
export function openNewDocumentTab(
  o: {
    parent: string;
    kind: DocumentKind;
    from: string;
    coarse: boolean;
    goTo: (to: string) => void;
    fail: (err: unknown) => string;
    // created runs once the server has made the file: the listing the new row belongs in is read again (decision 102).
    created?: () => void;
    // blocked runs when the browser refused the new tab, so the person is told how to allow it.
    blocked?: () => void;
  },
  open: (url: string, target: string) => unknown = (u, t) => window.open(u, t),
): void {
  const k = newKey();
  // The pending key is written before the tab opens, so the wait page finds it when it loads.
  startHandoff(k);
  const back = safeFrom(o.from);
  if (o.coarse) {
    o.goTo(waitPath(k));
  } else {
    // No noopener feature: a blocked pop-up returns null, and only then is nothing made. A tab that opened has its opener
    // link cleared here, before the new page runs anything, which keeps the protection noopener gives.
    const w = open(waitPath(k), '_blank') as Window | null | undefined;
    if (w === null || w === undefined) {
      o.blocked?.();
      return;
    }
    w.opener = null;
  }
  const tab = !o.coarse;
  void createDocument(o.parent, o.kind).then(
    (node) => {
      o.created?.();
      finishHandoff(k, { node: node.id, from: back, tab });
    },
    (err: unknown) => finishHandoff(k, { error: o.fail(err) }),
  );
}

// useNewDocument is the action the New menus call: it starts a document of kind in the place the New actions act on (the
// place the files page shows, when the page is the one the place is for: placeForRoute). Both the sidebar's New and the
// toolbar's New use it. An installation admin signed in as a person gets nothing: the server refuses the file.
export function useNewDocument(): (kind: DocumentKind) => void {
  const navigate = useNavigate();
  const location = useLocation();
  const lang = useLang();
  const { t } = useT();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const impersonating = !!useStore(session)?.impersonation;
  return useCallback(
    (kind: DocumentKind) => {
      if (impersonating) return;
      const place = placeForRoute(get(newPlace), location.pathname);
      if (!place?.nodeID) return;
      const branch = { folder: place.where.folder, dir: place.where.dirID };
      openNewDocumentTab({
        parent: place.nodeID,
        kind,
        from: location.pathname + location.search,
        coarse: coarsePointer(),
        goTo: (to) => navigate(to),
        fail: (err) => fileErrorText(err, lang),
        created: () => void refreshTree(qc, [branch]),
        blocked: () => void message.warning(t('files.newDocument.blocked')),
      });
    },
    [navigate, location.pathname, location.search, lang, qc, impersonating, message, t],
  );
}
