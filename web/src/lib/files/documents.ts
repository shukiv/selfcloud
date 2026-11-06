// SPDX-License-Identifier: AGPL-3.0-or-later

// New documents (decision 91): the New menu's Document, Spreadsheet and Presentation make a blank file in the folder shown
// and open it in the office editor (decision 72), in the same tab. The file is named by the server. A failure is reported and
// nothing is opened.
import { useCallback } from 'react';
import { App } from 'antd';
import { useQueryClient } from '@tanstack/react-query';
import { useLocation, useNavigate } from 'react-router-dom';
import { useLang } from '../i18n';
import { get } from '../store';
import { refreshTree } from '../query';
import { createDocument } from './api';
import { editorPath } from './editor';
import { fileErrorText } from './fileErrors';
import { newPlace } from './newPlace';
import type { Node } from './types';

export type DocumentKind = 'docx' | 'xlsx' | 'pptx';

export interface OpenNewDocument {
  // nodeID is the directory the file goes in, the folder's root or the directory shown.
  nodeID: string;
  kind: DocumentKind;
  // from is the place the editor returns to when it closes: the folder shown.
  from: string;
  // create makes the file. The default posts it to the server.
  create?: (nodeID: string, kind: DocumentKind) => Promise<Node>;
  navigate: (to: string) => void;
  // onCreated runs once the file exists, so the listing it returns to can read it again.
  onCreated?: (node: Node) => void;
  onError: (err: unknown) => void;
}

// openNewDocument makes the blank document and opens it in the editor. It resolves once the editor's address is set, or once
// the failure is reported.
export async function openNewDocument(o: OpenNewDocument): Promise<void> {
  let node: Node;
  try {
    node = await (o.create ?? createDocument)(o.nodeID, o.kind);
  } catch (err) {
    o.onError(err);
    return;
  }
  o.onCreated?.(node);
  o.navigate(editorPath(node.id, 'edit', o.from));
}

// useNewDocument is the action the New menus call: it makes a document of kind in the place the New actions act on (the
// place the files page shows) and opens it in the editor, in this tab. Both the sidebar's New and the toolbar's New use it.
export function useNewDocument(): (kind: DocumentKind) => void {
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const lang = useLang();
  return useCallback(
    (kind: DocumentKind) => {
      const place = get(newPlace);
      if (!place?.nodeID) return;
      const branch = { folder: place.where.folder, dir: place.where.dirID };
      void openNewDocument({
        nodeID: place.nodeID,
        kind,
        from: location.pathname + location.search,
        navigate: (to) => navigate(to),
        onCreated: () => void refreshTree(qc, [branch]),
        onError: (err) => void message.error(fileErrorText(err, lang)),
      });
    },
    [navigate, location.pathname, location.search, qc, message, lang],
  );
}
