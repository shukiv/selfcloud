// SPDX-License-Identifier: AGPL-3.0-or-later

import { atLeast, rank } from '../files/roles';
import type { Role } from '../files/types';

// The roles, lowest first (docs/design.md, section 8).
export const ROLES: Role[] = ['viewer', 'commenter', 'contributor', 'editor', 'manager'];

// shareCap is the highest role the caller may grant on a node, or null when it may not share there. A
// manager grants up to manager. An editor grants up to editor, and only where the node allows editors to
// share and the team space does not keep sharing for managers. Anyone else grants nothing. The server
// checks each change again; this only decides what the dialog offers.
export function shareCap(input: {
  role: Role | undefined;
  editorsCanShare: boolean;
  managersOnly: boolean;
}): Role | null {
  if (atLeast(input.role, 'manager')) return 'manager';
  if (input.role === 'editor' && input.editorsCanShare && !input.managersOnly) return 'editor';
  return null;
}

// grantableRoles lists the roles at or below cap, lowest first. A null cap grants none.
export function grantableRoles(cap: Role | null): Role[] {
  if (cap === null) return [];
  return ROLES.filter((r) => rank(r) <= rank(cap));
}
