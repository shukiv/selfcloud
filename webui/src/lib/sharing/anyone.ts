// SPDX-License-Identifier: AGPL-3.0-or-later

// The logic of a link that anyone can open: the state a visit is in, how the bearer is renewed, the expiry an
// anyone link gets, and the folder path a visitor sees. Components only show what these decide. The server
// checks every open, renewal and request again; nothing here is a security boundary.
import { ApiError } from '../apiError';
import { expiryFromInput } from './limits';

// The server's rules for anyone links (decision 2): an end date is required, 30 days by default, and at most
// 365 days ahead. A sharing.max_expiry smaller than that cap lowers it further.
export const ANYONE_DEFAULT_DAYS = 30;
export const ANYONE_MAX_DAYS = 365;

// A visitor credential lives 15 minutes. The page renews it this long before it ends.
export const RENEW_BEFORE_MS = 2 * 60 * 1000;

const DAY_MS = 24 * 60 * 60 * 1000;

// capDays is the longest an anyone link may run, in whole days. sharingMaxDays is GET /info's cap, 0 for none.
export function capDays(sharingMaxDays: number): number {
  return sharingMaxDays > 0 ? Math.min(ANYONE_MAX_DAYS, sharingMaxDays) : ANYONE_MAX_DAYS;
}

// defaultAnyoneEnd is the end a new anyone link gets when the person gives none.
export function defaultAnyoneEnd(now: Date, sharingMaxDays: number): Date {
  return new Date(now.getTime() + Math.min(ANYONE_DEFAULT_DAYS, capDays(sharingMaxDays)) * DAY_MS);
}

// latestAnyoneEnd is the latest instant an anyone link may end, counted from now.
export function latestAnyoneEnd(now: Date, sharingMaxDays: number): Date {
  return new Date(now.getTime() + capDays(sharingMaxDays) * DAY_MS);
}

// anyoneExpiresAt turns a date input's value into the instant an anyone link ends. The end of that day is taken,
// and a day that reaches past the cap is pulled back to the cap, so the server does not refuse a date the form
// offered. It returns null for an empty or malformed value, or for an end that is not in the future.
export function anyoneExpiresAt(value: string, now: Date, sharingMaxDays: number): string | null {
  const iso = expiryFromInput(value);
  if (iso === null) return null;
  const end = new Date(iso);
  if (!(end.getTime() > now.getTime())) return null;
  const latest = latestAnyoneEnd(now, sharingMaxDays);
  return (end.getTime() > latest.getTime() ? latest : end).toISOString();
}

// passwordProblem says why a new anyone password is refused, by the same count the server uses (characters, not
// bytes), or null when it is acceptable. An empty password means none, and is not a problem.
export function passwordProblem(password: string, minLength: number): 'short' | 'long' | null {
  if (password === '') return null;
  const n = [...password].length;
  if (n < minLength) return 'short';
  if (n > 256) return 'long';
  return null;
}

// Outcome is what an open answered, reduced to the cases the page tells apart.
export type OpenOutcome = 'opened' | 'password_required' | 'password_wrong' | 'rate_limited' | 'not_found' | 'failed';

// outcomeOf reads an open's refusal: a password the visitor must give, a wrong one, a lockout, a dead link, or a
// failure the page does not know. Anything that is not an API refusal is a failure.
export function outcomeOf(err: unknown): OpenOutcome {
  if (!(err instanceof ApiError)) return 'failed';
  if (err.code === 'rate_limited') return 'rate_limited';
  if (err.code === 'not_found') return 'not_found';
  if (err.code === 'unauthenticated' && err.details.reason === 'password_required') return 'password_required';
  if (err.code === 'unauthenticated' && err.details.reason === 'password_wrong') return 'password_wrong';
  return 'failed';
}

// VisitStep is the state of the link page.
export type VisitStep = 'bearer' | 'password' | 'password-wrong' | 'limited' | 'sign-in' | 'dead' | 'failed';

// visitStep says what the page shows after an open. A dead link sends a signed-out visitor to sign in, since
// the link may be one for an account; a signed-in visitor gets the one dead-link message.
export function visitStep(outcome: OpenOutcome, signedIn: boolean): VisitStep {
  switch (outcome) {
    case 'opened':
      return 'bearer';
    case 'password_required':
      return 'password';
    case 'password_wrong':
      return 'password-wrong';
    case 'rate_limited':
      return 'limited';
    case 'not_found':
      return signedIn ? 'dead' : 'sign-in';
    case 'failed':
      return 'failed';
  }
}

// renewDelay is how long to wait before the credential is renewed: RENEW_BEFORE_MS before it ends, or at once
// when it is already that close.
export function renewDelay(expiresAt: string, now: number): number {
  const at = Date.parse(expiresAt);
  if (Number.isNaN(at)) return 0;
  return Math.max(0, at - RENEW_BEFORE_MS - now);
}

// NodeRef is what the folder path needs of a node.
export interface NodeRef {
  id: string;
  parent_id: string | null;
  name: string;
}

// crumbsTo lists the nodes from the link's root down to current, root first. It stops at the root, so a visitor
// sees nothing above the link. It returns null when a node on the way is unknown, or the root is never reached.
export function crumbsTo(nodes: ReadonlyMap<string, NodeRef>, current: string, root: string): NodeRef[] | null {
  const out: NodeRef[] = [];
  let id: string | null = current;
  for (let depth = 0; id !== null && depth < 10_000; depth++) {
    const n = nodes.get(id);
    if (!n) return null;
    out.unshift(n);
    if (n.id === root) return out;
    id = n.parent_id;
  }
  return null;
}

export type Previewable = 'image' | 'video' | 'audio' | 'pdf';

// previewKind says how a file is shown inline, from its MIME type, or null when the page shows no preview.
export function previewKind(mime: string | undefined): Previewable | null {
  if (!mime) return null;
  const t = mime.toLowerCase().split(';')[0].trim();
  if (t.startsWith('image/')) return 'image';
  if (t.startsWith('video/')) return 'video';
  if (t.startsWith('audio/')) return 'audio';
  if (t === 'application/pdf') return 'pdf';
  return null;
}
