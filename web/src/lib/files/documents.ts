// SPDX-License-Identifier: AGPL-3.0-or-later

// New documents (decision 91, round 2 R5): the New menu's Document, Spreadsheet and Presentation open a new tab at /edit/new,
// which makes the blank file and replaces its own address with the editor's (decision 102). The file is named by the server.
// The opener makes nothing itself: a pick the browser blocks, or a failure, is shown in the new tab. On a coarse pointer the
// same page opens in this tab.
import { useCallback } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { get } from '../store';
import { coarsePointer, safeFrom } from './editor';
import { newPlace, placeForRoute } from './newPlace';

export type DocumentKind = 'docx' | 'xlsx' | 'pptx';

const KINDS: readonly DocumentKind[] = ['docx', 'xlsx', 'pptx'];
const NOD = /^nod_[A-Za-z0-9_-]+$/;

// NewDocumentAsk is what the /edit/new page reads from its address: the kind, the node the file goes in, and the place to
// return to when the editor closes. tab says the page was opened in a new tab by a script.
export interface NewDocumentAsk {
  kind: DocumentKind;
  parent: string;
  from: string | null;
  tab: boolean;
}

// newDocumentPath is the address of the new-document page. The kind and the parent are the server's names, never a path.
export function newDocumentPath(parent: string, kind: DocumentKind, from: string | null, tab: boolean): string {
  const q = new URLSearchParams({ kind, parent });
  const back = safeFrom(from);
  if (back !== null) q.set('from', back);
  if (tab) q.set('tab', '1');
  return `/edit/new?${q.toString()}`;
}

// readNewDocument reads the new-document page's query. A kind that is not one of the three, or a parent that is not a node's
// ID, is no request: null, and the page makes nothing.
export function readNewDocument(search: string): NewDocumentAsk | null {
  const q = new URLSearchParams(search);
  const kind = q.get('kind') as DocumentKind | null;
  const parent = q.get('parent') ?? '';
  if (kind === null || !KINDS.includes(kind) || !NOD.test(parent)) return null;
  return { kind, parent, from: safeFrom(q.get('from')), tab: q.get('tab') === '1' };
}

// openNewDocumentTab opens the new-document page for a parent and a kind. On a fine pointer it is a new tab with noopener, from
// the person's click, so the browser allows it; on a coarse pointer it is this tab. Nothing is created here.
export function openNewDocumentTab(
  o: { parent: string; kind: DocumentKind; from: string; coarse: boolean; goTo: (to: string) => void },
  open: (url: string, target: string, features: string) => unknown = (u, t, f) => window.open(u, t, f),
): void {
  if (o.coarse) {
    o.goTo(newDocumentPath(o.parent, o.kind, o.from, false));
    return;
  }
  open(newDocumentPath(o.parent, o.kind, null, true), '_blank', 'noopener');
}

// useNewDocument is the action the New menus call: it opens a new tab for a document of kind in the place the New actions act
// on (the place the files page shows, when the page is the one the place is for: placeForRoute). Both the sidebar's New and the
// toolbar's New use it.
export function useNewDocument(): (kind: DocumentKind) => void {
  const navigate = useNavigate();
  const location = useLocation();
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
      });
    },
    [navigate, location.pathname, location.search],
  );
}
