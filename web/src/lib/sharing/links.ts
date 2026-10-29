// SPDX-License-Identifier: AGPL-3.0-or-later

// The logic of share links: the shape the server answers, the roles a dialog may offer for a link, and the
// handling of a secret from a link's address. The components only show what these decide. The server checks
// every change and every open again.
import { ROLES } from './roles';
import type { Role } from '../files/types';

// ShareLink is a link as GET and the change calls answer it. inherited_from is the node the link sits on,
// when that is not the node asked about; it is null on the node itself. The sharer-only fields are absent for
// a member who only reads.
export interface ShareLink {
  id: string;
  node_id: string;
  audience: 'organisation' | 'anyone';
  role: Role;
  expires_at: string | null;
  created_at: string;
  inherited_from: string | null;
  created_by?: string;
  visitors?: number;
  paused?: boolean;
}

// ShareLinkCreated is the answer to a new link or a reset: the link, and the URL that opens it. The URL is
// shown once.
export interface ShareLinkCreated {
  link: ShareLink;
  url: string;
}

// linkRoleOptions lists the roles a link may give, lowest first, for a caller whose share cap is cap (null
// when it may not share). A link never gives manager, so the list stops at editor.
export function linkRoleOptions(cap: Role | null): Role[] {
  if (cap === null) return [];
  const top = ROLES.indexOf(cap);
  return ROLES.slice(0, top + 1).filter((r) => r !== 'manager');
}

// ownLink is the link on the node itself, or null when it has none.
export function ownLink(links: ShareLink[], nodeID: string): ShareLink | null {
  return links.find((l) => l.node_id === nodeID && (l.inherited_from ?? null) === null) ?? null;
}

// inheritedLinks are the links above the node that the caller may see, which the dialog shows as inherited.
export function inheritedLinks(links: ShareLink[], nodeID: string): ShareLink[] {
  return links.filter((l) => l.node_id !== nodeID || (l.inherited_from ?? null) !== null);
}

// secretPattern is the shape of a secret the server issues: 32 random bytes in unpadded base64url.
const secretPattern = /^[A-Za-z0-9_-]{43}$/;

// secretFromAddress reads a link's secret from its address fragment ("#secret"). It returns "" when the
// fragment is absent or not the shape a secret has, so nothing else is sent to the server.
export function secretFromAddress(hash: string): string {
  const s = hash.startsWith('#') ? hash.slice(1) : hash;
  return secretPattern.test(s) ? s : '';
}

// pendingKey is where a secret waits in this tab, between the moment the link is opened and the moment
// it is used. It is session storage, so another tab never sees it.
export const pendingKey = 'selfcloud.share.secret';

// Keep is the part of the session storage the link page uses. A test passes a plain object.
export interface Keep {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

// captureSecret moves the secret out of the address, into storage, and strips the address at once. It
// reports whether a secret was found. Storage that refuses the write leaves no secret behind, and the
// address is stripped all the same.
export function captureSecret(
  hash: string,
  pathname: string,
  search: string,
  keep: Keep,
  replace: (url: string) => void,
): boolean {
  const secret = secretFromAddress(hash);
  replace(pathname + search);
  if (secret === '') return false;
  try {
    keep.setItem(pendingKey, JSON.stringify({ secret, at: Date.now() }));
    return true;
  } catch {
    return false;
  }
}

// pendingTTL is how long a captured secret waits. A visit that does not finish in this time starts again from
// the link, so a secret does not sit in the tab for a whole session.
export const pendingTTL = 10 * 60_000;

// peekSecret reads a waiting secret without removing it. It is "" when none waits, when storage is refused, or
// when the secret waited longer than pendingTTL: an old one is removed at once.
export function peekSecret(keep: Keep, now: number = Date.now()): string {
  try {
    const raw = keep.getItem(pendingKey);
    if (raw === null) return '';
    const v = parsePending(raw);
    if (v === null || !secretPattern.test(v.secret)) return '';
    if (now - v.at > pendingTTL) {
      forgetSecret(keep);
      return '';
    }
    return v.secret;
  } catch {
    return '';
  }
}

// parsePending reads what captureSecret stored, or null for anything else.
function parsePending(raw: string): { secret: string; at: number } | null {
  try {
    const v: unknown = JSON.parse(raw);
    if (typeof v !== 'object' || v === null) return null;
    const { secret, at } = v as { secret?: unknown; at?: unknown };
    if (typeof secret !== 'string' || typeof at !== 'number') return null;
    return { secret, at };
  } catch {
    return null;
  }
}

// forgetSecret removes a waiting secret, once it is used or has failed.
export function forgetSecret(keep: Keep): void {
  try {
    keep.removeItem(pendingKey);
  } catch {
    // Nothing to remove when storage refuses.
  }
}

// openStep says what the link page does next: open the link, send a signed-out visitor to sign in
// (the secret waits), or show the one message a dead link gets (no secret waits).
export type OpenStep = 'open' | 'sign-in' | 'dead';

export function openStep(secret: string, signedIn: boolean): OpenStep {
  if (secret === '') return 'dead';
  return signedIn ? 'open' : 'sign-in';
}
