// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from './types';

// NodeLinkPlan says where /n/{nod} goes. in-folder opens a folder's listing (a directory or a file's parent),
// with a file's preview or a directory selected in it. folder opens a folder's root. alone shows a file that
// the caller can reach while its folder listing is not reachable: the file is shown with nothing behind it.
export type NodeLinkPlan =
  | { kind: 'folder'; folder: string }
  | { kind: 'in-folder'; folder: string; dir: string | null; preview?: string; select?: string }
  | { kind: 'alone'; node: Node };

// planNodeLink decides where a node's address goes. node is the node the caller reached. parent is the node's
// parent when the caller can reach it, and null when not. The parent's own parent is null for a folder's root,
// which is what makes the parent the root and not a directory.
export function planNodeLink(node: Node, parent: Node | null): NodeLinkPlan {
  if (node.parent_id === null) return { kind: 'folder', folder: node.folder_id };
  if (parent === null && node.kind === 'shortcut') {
    // A shortcut is never a directory of its own: with its folder out of reach there is nothing to select it in.
    return { kind: 'folder', folder: node.folder_id };
  }
  if (parent === null) {
    // The folder listing cannot be reached. A file is shown on its own; a directory is opened on its own,
    // since a shared directory's folder root is not reachable and would answer not found.
    if (node.kind === 'file') return { kind: 'alone', node };
    return { kind: 'in-folder', folder: node.folder_id, dir: node.id };
  }
  const dir = parent.parent_id !== null ? parent.id : null;
  if (node.kind === 'file') return { kind: 'in-folder', folder: node.folder_id, dir, preview: node.id };
  return { kind: 'in-folder', folder: node.folder_id, dir, select: node.id };
}
