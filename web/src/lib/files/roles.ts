// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node, Role } from './types';

// The roles, lowest first. Each includes what is below it (docs/design.md, section 8).
const ORDER: Role[] = ['viewer', 'commenter', 'contributor', 'editor', 'manager'];

// rank is a role's place on the ladder; an unknown or missing role ranks below viewer.
export function rank(role: string | null | undefined): number {
  const i = ORDER.indexOf(role as Role);
  return i < 0 ? -1 : i;
}

export function atLeast(role: string | null | undefined, need: Role): boolean {
  return rank(role) >= rank(need);
}

// The operations the interface offers, each with the role the API needs (the operations table).
export const can = {
  // Creating a folder or uploading into a directory: contributor on it.
  create: (role: string | null | undefined) => atLeast(role, 'contributor'),
  // Rename, delete and move out of a directory: editor on the item.
  edit: (role: string | null | undefined) => atLeast(role, 'editor'),
  // Copy: viewer on the source (the destination is checked on its own).
  read: (role: string | null | undefined) => atLeast(role, 'viewer'),
  // Restore an earlier version: contributor. Label, keep forever, delete a version: editor.
  restoreVersion: (role: string | null | undefined) => atLeast(role, 'contributor'),
  editVersion: (role: string | null | undefined) => atLeast(role, 'editor'),
};

// downloadAllowed is false only where the no-download rule can bind: a viewer or commenter on a node
// with no_download set. An inherited flag does not show in the node, so the download checks the
// server too (see download.ts).
export function downloadAllowed(node: Pick<Node, 'no_download' | 'role'>): boolean {
  if (!node.no_download) return true;
  return atLeast(node.role, 'contributor');
}
