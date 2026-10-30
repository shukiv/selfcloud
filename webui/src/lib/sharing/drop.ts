// SPDX-License-Identifier: AGPL-3.0-or-later

// The logic of Request files: a drop link lets anyone send files into one directory, and see nothing else. The
// dialog's form becomes the body the server takes, and the drop page's uploader fields become the upload's
// metadata. Components only show what these decide. The server checks every rule again; nothing here is a
// security boundary.
import type { Role } from '../files/types';
import type { ShareLink } from './links';
import { anyoneExpiresAt, defaultAnyoneEnd, latestAnyoneEnd, passwordProblem } from './anyone';
import { dateInputValue } from './limits';

// The server's limits for a drop link's text and for the uploader's fields, in characters.
export const DROP_TITLE_MAX = 100;
export const DROP_MESSAGE_MAX = 1000;
export const UPLOADER_NAME_MAX = 100;
export const UPLOADER_EMAIL_MAX = 254;

const DAY_MS = 24 * 60 * 60 * 1000;
const LINE_BREAK = /\r\n|\r|\n/g;
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// canRequestFiles says whether the dialog offers Request files: the account allows links that anyone can open, and
// the caller is a manager of the item. An editor never sees it, even where editors may share.
export function canRequestFiles(publicLinks: boolean, cap: Role | null): boolean {
  return publicLinks && cap === 'manager';
}

// cleanLine turns every line break into a space and trims the ends. A line break in a title or a name would start a
// new line in a mail or a header, so none is kept.
export function cleanLine(text: string): string {
  return text.replace(LINE_BREAK, ' ').trim();
}

// tooLong says whether text has more characters than max. Characters are counted as code points, as the server
// counts them.
export function tooLong(text: string, max: number): boolean {
  return [...text].length > max;
}

// DropForm is what the dialog's Request files form holds. date is the end date input's value. passwordChanged is
// set when the person typed a password to save; an empty password with it set removes the password.
export interface DropForm {
  date: string;
  password: string;
  passwordChanged: boolean;
  title: string;
  message: string;
  askName: boolean;
  notify: boolean;
}

// DropProblem names the one thing the form must change before it is sent.
export type DropProblem = 'date' | 'title_long' | 'message_long' | 'password_short' | 'password_long';

// dropDefaultDate is the end date a new drop link gets: 30 days ahead, or the sharing cap when that is shorter.
export function dropDefaultDate(now: Date, maxDays: number): string {
  return dateInputValue(defaultAnyoneEnd(now, maxDays));
}

// dropDateLimits are the earliest and latest dates the end date input offers: tomorrow, and the cap.
export function dropDateLimits(now: Date, maxDays: number): { earliest: string; latest: string } {
  return {
    earliest: dateInputValue(new Date(now.getTime() + DAY_MS)),
    latest: dateInputValue(latestAnyoneEnd(now, maxDays)),
  };
}

// dropProblem says what must change before the form can be sent, or null when it can. The end date is required:
// a drop link always ends, as an anyone link does. The password rule is the one anyone links use.
export function dropProblem(f: DropForm, now: Date, maxDays: number, minPassword: number): DropProblem | null {
  if (anyoneExpiresAt(f.date, now, maxDays) === null) return 'date';
  if (tooLong(cleanLine(f.title), DROP_TITLE_MAX)) return 'title_long';
  if (tooLong(f.message, DROP_MESSAGE_MAX)) return 'message_long';
  if (f.passwordChanged) {
    const p = passwordProblem(f.password, minPassword);
    if (p === 'short') return 'password_short';
    if (p === 'long') return 'password_long';
  }
  return null;
}

// dropCreateBody is the body of POST /nodes/{nod}/share-links that makes a drop link. It is null when the form has a
// problem. A password is sent only when one is typed.
export function dropCreateBody(
  f: DropForm,
  now: Date,
  maxDays: number,
  minPassword: number,
): Record<string, unknown> | null {
  if (dropProblem(f, now, maxDays, minPassword) !== null) return null;
  const expires = anyoneExpiresAt(f.date, now, maxDays);
  if (expires === null) return null;
  const body: Record<string, unknown> = {
    audience: 'anyone',
    upload_only: true,
    expires_at: expires,
    title: cleanLine(f.title),
    message: f.message,
    ask_name: f.askName,
    notify: f.notify,
  };
  if (f.password !== '') body.password = f.password;
  return body;
}

// dropPatchBody is the body that changes a drop link. Only the fields that can change are sent: the end date, the
// text, the two switches and, when the person changed it, the password ("" removes it).
export function dropPatchBody(
  f: DropForm,
  now: Date,
  maxDays: number,
  minPassword: number,
): Record<string, unknown> | null {
  if (dropProblem(f, now, maxDays, minPassword) !== null) return null;
  const expires = anyoneExpiresAt(f.date, now, maxDays);
  if (expires === null) return null;
  const body: Record<string, unknown> = {
    expires_at: expires,
    title: cleanLine(f.title),
    message: f.message,
    ask_name: f.askName,
    notify: f.notify,
  };
  if (f.passwordChanged) body.password = f.password;
  return body;
}

// Uploader is the name and email a visitor gives on the drop page. Both are optional unless the link asks for them.
export interface Uploader {
  name: string;
  email: string;
}

// UploaderProblem names what the drop page must change before it uploads.
export type UploaderProblem = 'name_required' | 'email_required' | 'name_long' | 'email_long' | 'email_invalid';

// uploaderProblem checks what a visitor typed. When the link asks for them, a name and an email are required. The
// email is never verified and never mailed, so its check is only its shape.
export function uploaderProblem(askName: boolean, u: Uploader): UploaderProblem | null {
  const name = cleanLine(u.name);
  const email = cleanLine(u.email);
  if (askName && name === '') return 'name_required';
  if (askName && email === '') return 'email_required';
  if (tooLong(name, UPLOADER_NAME_MAX)) return 'name_long';
  if (tooLong(email, UPLOADER_EMAIL_MAX)) return 'email_long';
  if (email !== '' && !EMAIL_SHAPE.test(email)) return 'email_invalid';
  return null;
}

// uploaderFields is what is sent with each file: the name and email with their line breaks cleaned, and an empty
// value left out of the metadata.
export function uploaderFields(u: Uploader): Uploader {
  return { name: cleanLine(u.name), email: cleanLine(u.email) };
}

// dropMetadata is the Upload-Metadata of one file sent through a drop link. The name is the file's own name. The
// server takes the type from the name, and places the file itself: no folder, parent, path or conflict setting is
// sent.
export function dropMetadata(fileName: string, u: Uploader): Record<string, string | undefined> {
  const fields = uploaderFields(u);
  return {
    name: fileName,
    uploader_name: fields.name || undefined,
    uploader_email: fields.email || undefined,
  };
}

// DropInfo is what GET /share/info says about a drop link to the visitor who holds its credential.
export interface DropInfo {
  title: string;
  message: string;
  ask_name: boolean;
  expires_at: string;
  max_file_size: number;
}

// dropTitle is the heading of the drop page: the link's title, or the default when it has none.
export function dropTitle(info: Pick<DropInfo, 'title'>, fallback: string): string {
  const t = cleanLine(info.title);
  return t === '' ? fallback : t;
}

// DropState is what the dialog shows of a directory's drop link: none, a live one, or one policy pauses.
export type DropState = 'off' | 'on' | 'paused';

// dropState says what the dialog shows for the drop link the directory has, if any.
export function dropState(link: ShareLink | null): DropState {
  if (link === null) return 'off';
  return link.paused === true ? 'paused' : 'on';
}

// dropFormOf reads the form from the drop link the server holds. The password is never shown back, so the form
// starts with none, and only a password typed afterwards is sent. dateOf turns the instant into a date input value.
export function dropFormOf(link: ShareLink, dateOf: (iso: string) => string): DropForm {
  return {
    date: link.expires_at ? dateOf(link.expires_at) : '',
    password: '',
    passwordChanged: false,
    title: link.title ?? '',
    message: link.message ?? '',
    askName: link.ask_name === true,
    notify: link.notify === true,
  };
}
