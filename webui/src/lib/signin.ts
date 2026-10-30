// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from './apiError';
import { afterSignIn } from './next';
import type { User } from './types';

// tokenFromFragment reads the sign-in token from a URL fragment such as "#token=...". It returns ""
// when there is none.
export function tokenFromFragment(hash: string): string {
  return new URLSearchParams(hash.startsWith('#') ? hash.slice(1) : hash).get('token') ?? '';
}

// looksLikeToken reports whether s has the shape the server issues: 32 random bytes in unpadded
// base64url. A value of any other shape is refused without a request.
export function looksLikeToken(s: string): boolean {
  return /^[A-Za-z0-9_-]{43}$/.test(s);
}

// signInTarget is the page a sign-in opens: the path the server gave, checked again here, or the
// home page of files when that path is not usable or is the root. An admin's root is its own home.
export function signInTarget(next: string | null | undefined, user?: User): string {
  const path = afterSignIn(next ?? '', user);
  return path === '/' ? '/files' : path;
}

// SwitchPrompt names the two users in a sign-in link's question: the one this browser is signed in as, and
// the one the link signs in.
export interface SwitchPrompt {
  current: string;
  target: string;
}

// switchPromptOf reads the question from a 409 signed_in_as_other answer. It is null for any other error, or
// when a name is missing, so the page then shows its usual message.
export function switchPromptOf(err: unknown): SwitchPrompt | null {
  if (!(err instanceof ApiError) || err.code !== 'conflict') return null;
  if (err.details.reason !== 'signed_in_as_other') return null;
  const current = shownName(err.details.current);
  const target = shownName(err.details.target);
  if (current === null || target === null) return null;
  return { current, target };
}

// shownName is the name a user is shown by, the name or else the email, from the server's answer.
function shownName(v: unknown): string | null {
  if (typeof v !== 'object' || v === null) return null;
  const { name, email } = v as { name?: unknown; email?: unknown };
  if (typeof name === 'string' && name !== '') return name;
  if (typeof email === 'string' && email !== '') return email;
  return null;
}

// Part is a run of a sentence: a name, which the page wraps in bdi, or other text.
export interface Part {
  text: string;
  name: boolean;
}

// splitParts cuts a catalogue sentence at its placeholders that names fills, so each name can be shown
// in its own bdi element. Other placeholders stay in the text. The parts are shown as text, never parsed
// as HTML.
export function splitParts(template: string, names: Record<string, string>): Part[] {
  const out: Part[] = [];
  const re = /\{([A-Za-z]+)\}/g;
  let last = 0;
  for (let m = re.exec(template); m !== null; m = re.exec(template)) {
    if (!Object.prototype.hasOwnProperty.call(names, m[1])) continue;
    if (m.index > last) out.push({ text: template.slice(last, m.index), name: false });
    out.push({ text: names[m[1]], name: true });
    last = m.index + m[0].length;
  }
  if (last < template.length) out.push({ text: template.slice(last), name: false });
  return out;
}
