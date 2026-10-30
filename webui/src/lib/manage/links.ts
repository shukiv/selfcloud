// SPDX-License-Identifier: AGPL-3.0-or-later

// The account-wide list of share links, as the owner's Links page shows it. The server decides what each
// row may show (item_name and item_path are null when hidden); this module only turns a row into the words
// and the query the page uses. Labels are returned as translation keys or as plain text, so the module
// stays pure and testable.

// AccountLink is one row of GET /api/v1/accounts/{acc}/share-links.
export interface AccountLink {
  id: string;
  node_id: string;
  audience: 'organisation' | 'anyone';
  // role is '' for a drop link.
  role: string;
  upload_only: boolean;
  expires_at: string | null;
  created_at: string;
  paused: boolean;
  password_set: boolean;
  created_by: string;
  created_by_kind: 'user' | 'account_key';
  created_by_name: string;
  // visitors is set for organisation links only.
  visitors?: number;
  folder_id: string;
  folder_kind: 'team' | 'personal';
  folder_name: string;
  folder_owner_id: string;
  folder_owner_name: string;
  item_name: string | null;
  item_path: string | null;
  hidden: boolean;
}

export interface AccountLinkPage {
  items: AccountLink[];
  next_cursor?: string;
}

export type AudienceFilter = 'all' | 'organisation' | 'anyone';
export type KindFilter = 'all' | 'access' | 'drop';

export interface LinkFilter {
  audience: AudienceFilter;
  kind: KindFilter;
}

export const NO_FILTER: LinkFilter = { audience: 'all', kind: 'all' };

// Label is a piece of text the page shows: plain text (a name from the server), or a translation key with its
// parameters.
export type Label = { text: string } | { key: string; params?: Record<string, string> };

// linkListQuery is the query string for one page of the list. The filters that say "all" are left out.
export function linkListQuery(filter: LinkFilter, cursor: string | undefined, limit: number): string {
  const q = new URLSearchParams();
  if (filter.audience !== 'all') q.set('audience', filter.audience);
  if (filter.kind !== 'all') q.set('kind', filter.kind);
  q.set('limit', String(limit));
  if (cursor) q.set('cursor', cursor);
  return `?${q.toString()}`;
}

// mergeLinks adds a page to the rows shown. A reset (the first page after a filter change or a delete) replaces
// them; otherwise the page's rows follow the rows already shown.
export function mergeLinks(shown: AccountLink[], page: AccountLinkPage, reset: boolean): AccountLink[] {
  if (reset) return page.items;
  const seen = new Set(shown.map((l) => l.id));
  return [...shown, ...page.items.filter((l) => !seen.has(l.id))];
}

// audienceLabel says who can open a link.
export function audienceLabel(l: AccountLink): Label {
  return l.audience === 'anyone' ? { key: 'links.audience.anyone' } : { key: 'links.audience.organisation' };
}

// kindLabel says what kind of link it is: an access link, or a drop link that takes uploads only.
export function kindLabel(l: AccountLink): Label {
  return l.upload_only ? { key: 'links.kind.drop' } : { key: 'links.kind.access' };
}

// itemLabel is the name of the item a link sits on. An item the server hides shows who keeps it (a member's
// My files) or, for an installation admin, that it is not shown at all.
export function itemLabel(l: AccountLink, admin: boolean): Label {
  if (l.hidden || l.item_name === null) {
    if (admin) return { key: 'links.item.hiddenAdmin' };
    return { key: 'links.item.hiddenMember', params: { member: l.folder_owner_name } };
  }
  return { text: l.item_name };
}

// pathLabel is the item's path, or nothing when the server hides it.
export function pathLabel(l: AccountLink): string {
  return l.item_path ?? '';
}

// folderLabel names the place the item is in: the team space's name, the viewer's own My files, or another
// member's My files.
export function folderLabel(l: AccountLink, viewerID: string): Label {
  if (l.folder_kind === 'team') return { text: l.folder_name };
  if (l.folder_owner_id !== '' && l.folder_owner_id === viewerID) return { key: 'links.folder.mine' };
  return { key: 'links.folder.memberFiles', params: { name: l.folder_owner_name } };
}

// creatorLabel says who made the link: a person by name, or an account key.
export function creatorLabel(l: AccountLink): Label {
  if (l.created_by_kind === 'account_key') return { key: 'links.by.key' };
  return { key: 'links.by.user', params: { name: l.created_by_name } };
}

// statusKeys lists the words that describe a link's state: paused by policy or a trashed item, a password, and
// the visitor count of an organisation link. The end date is left to the page, which formats it.
export function statusKeys(l: AccountLink): Label[] {
  const out: Label[] = [];
  if (l.paused) out.push({ key: 'links.status.paused' });
  if (l.password_set) out.push({ key: 'links.status.password' });
  if (l.audience === 'organisation' && typeof l.visitors === 'number') {
    out.push({ key: 'links.status.visitors', params: { count: String(l.visitors) } });
  }
  return out;
}

// deleteAllCount is the number the confirmation names: a whole number, never negative.
export function deleteAllCount(n: number): number {
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

// canDeleteAll says whether "Delete all public links" is offered at all: there must be a link to delete, and no
// delete in progress.
export function canDeleteAll(count: number, busy: boolean): boolean {
  return deleteAllCount(count) > 0 && !busy;
}

// removeLink drops a deleted link from the rows shown.
export function removeLink(shown: AccountLink[], id: string): AccountLink[] {
  return shown.filter((l) => l.id !== id);
}
