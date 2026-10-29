// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Permission } from '../files/types';

// Origin says where a permission sits, as the dialog shows it: on this item, on an ancestor the caller can
// name (its name is known), or on an ancestor it cannot reach (the dialog says only "a parent folder").
export type Origin = { kind: 'own' } | { kind: 'named'; id: string; name: string } | { kind: 'parent'; id: string };

// originOf works out a permission's origin. names maps the IDs of the ancestors the caller could read to
// their names; an ancestor missing from names is one it cannot reach.
export function originOf(perm: Pick<Permission, 'inherited_from'>, names: ReadonlyMap<string, string>): Origin {
  const from = perm.inherited_from;
  if (from === null) return { kind: 'own' };
  const name = names.get(from);
  if (name !== undefined) return { kind: 'named', id: from, name };
  return { kind: 'parent', id: from };
}

// canRemoveHere is true only for a permission on the item itself. An inherited one is removed on the
// ancestor it sits on, so the dialog offers "open the parent" instead.
export function canRemoveHere(perm: Pick<Permission, 'inherited_from'>): boolean {
  return perm.inherited_from === null;
}

// ancestorIDs lists the distinct ancestors the permissions sit on, so the dialog can name the ones it can read.
export function ancestorIDs(perms: ReadonlyArray<Pick<Permission, 'inherited_from'>>): string[] {
  const seen = new Set<string>();
  for (const p of perms) if (p.inherited_from !== null) seen.add(p.inherited_from);
  return [...seen];
}
