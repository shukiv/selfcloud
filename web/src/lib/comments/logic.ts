// SPDX-License-Identifier: AGPL-3.0-or-later

// The rules of the comments, activity and who-viewed panels, as plain functions so they can be tested without a page.
// The server decides every permission; these only decide what to offer, and what a refusal means to the person.

import { ApiError } from "../apiError";
import { atLeast } from "../files/roles";
import { sentence, type AuditEntry, type Names, type Sentence } from "../audit/sentence";
import type { Comment, ActivityEntry, Viewer, Page, Person } from "./api";

// The limits the server sets (decision 39). The panel checks them before a send, so a person sees the rule and not a
// refusal from the server.
export const MAX_CHARS = 2000;
export const MAX_LINES = 50;
export const MAX_MENTIONS = 10;

// canComment: a commenter and up may write, reply and resolve (decision 39, who may comment).
export const canComment = (role: string | null | undefined): boolean => atLeast(role, "commenter");

// canEdit: the author only, and only while still a commenter (the server checks both).
export function canEdit(c: Comment, role: string | null | undefined): boolean {
  return c.mine === true && !c.deleted && canComment(role);
}

// canDelete: the author, or a manager of the item. The server checks the same rule inside the write.
export function canDelete(c: Comment, role: string | null | undefined): boolean {
  return !c.deleted && (c.mine === true || role === "manager");
}

// canReply and canResolve work on a top-level comment only: a reply has no replies, and only a thread is resolved.
export const canReply = (c: Comment, role: string | null | undefined): boolean => !c.parent_id && !c.deleted && canComment(role);
export const canResolve = (c: Comment, role: string | null | undefined): boolean => !c.parent_id && !c.deleted && canComment(role);

// Mentions. A person is named in the text as @name, and sent as the reference the people list gave. A person whose
// name the author removed from the text is not sent.
export interface Chosen {
  ref: string;
  name: string;
}

// insertMention adds @name to the end of the text, with the space that keeps it a word of its own.
export function insertMention(text: string, name: string): string {
  const sep = text === "" || /\s$/.test(text) ? "" : " ";
  return `${text}${sep}@${name} `;
}

// mentionRefs gives the references of the chosen people whose name is still in the text, each once.
export function mentionRefs(text: string, chosen: Chosen[]): string[] {
  const out: string[] = [];
  for (const c of chosen) {
    if (text.includes(`@${c.name}`) && !out.includes(c.ref)) out.push(c.ref);
  }
  return out;
}

// Characters are counted as code points, after NFC, as the server counts them.
export function charCount(text: string): number {
  return Array.from(text.normalize("NFC")).length;
}

export function lineCount(text: string): number {
  return text.split(/\r\n|\r|\n/).length;
}

// checkBody is the first rule a body breaks, as a text key and its number, or null when the body may be sent.
export function checkBody(text: string, refs: string[]): { key: string; params?: Record<string, number> } | null {
  if (text.trim() === "") return { key: "comments.blank" };
  if (charCount(text) > MAX_CHARS) return { key: "comments.tooLong", params: { max: MAX_CHARS } };
  if (lineCount(text) > MAX_LINES) return { key: "comments.tooLines", params: { max: MAX_LINES } };
  if (refs.length > MAX_MENTIONS) return { key: "comments.tooMentions", params: { max: MAX_MENTIONS } };
  return null;
}

// The names the people search answers with: at most five, each once by reference.
export function uniquePeople(items: Person[]): Person[] {
  const seen = new Set<string>();
  const out: Person[] = [];
  for (const p of items) {
    if (seen.has(p.ref)) continue;
    seen.add(p.ref);
    out.push(p);
  }
  return out;
}

// Paging. The next page is asked for only while the server gives a cursor.
export function nextPage<T>(last: Page<T>): string | undefined {
  return last.next_cursor ? last.next_cursor : undefined;
}

// An activity page can be empty and still carry a cursor (the server reads a window of its rows, and a rare kind may
// match none of them). The read follows such cursors until a page has entries or no cursor comes. The rounds are
// bounded, so a server that keeps answering with cursors cannot hold the page forever; the last cursor is then left for
// a "show more" press.
export const MAX_EMPTY_ROUNDS = 20;

export async function followEmpty<T>(
  fetchPage: (cursor?: string) => Promise<Page<T>>,
  cursor?: string,
  rounds = MAX_EMPTY_ROUNDS,
): Promise<Page<T>> {
  let page = await fetchPage(cursor);
  let n = 0;
  while (page.items.length === 0 && page.next_cursor && n < rounds) {
    page = await fetchPage(page.next_cursor);
    n++;
  }
  return page;
}

// Activity. The activity route's entries have the actor as a name and flags; the audit sentences (lib/audit/sentence.ts)
// want an actor as a kind and an ID, and a name from a lookup. The adapter gives the sentence the one name it needs.
// A person who is gone is shown as "former member", and a person past the server's bound is named by no one.
const ACTOR = "actor";

export interface ActivityText {
  sentence: Sentence;
  // formerLabel replaces the actor when the entry's actor is gone.
  gone: boolean;
}

export function activitySentence(entry: ActivityEntry, youWord: string, formerWord: string): ActivityText {
  const a = entry.actor;
  let kind: AuditEntry["actor"]["kind"];
  const details: Record<string, unknown> = { ...(entry.details ?? {}) };
  switch (a.kind) {
    case "user":
      kind = "user";
      break;
    case "account_key":
      kind = "account_key";
      break;
    case "system":
      kind = "system";
      break;
    case "admin":
      // An installation admin acted as the person. The sentence names an admin, not the person.
      kind = "user";
      details.actor_admin = true;
      break;
    default:
      kind = "other";
  }
  const audit: AuditEntry = { action: entry.action, actor: { kind, id: ACTOR }, target: "", details };
  const names: Names = {
    person: (id: string) => (id === ACTOR && kind === "user" && a.kind === "user" ? (a.mine ? youWord : a.name) : undefined),
    space: () => undefined,
  };
  // A gone person has no name: the sentence falls back to a generic word, which is then replaced by the former-member text.
  const s = sentence(audit, names);
  const gone = a.kind === "user" && a.gone === true;
  if (gone) s.params[ACTOR] = { text: formerWord };
  return { sentence: s, gone };
}

// Who viewed. A row is named while the person reaches the item, is gone when the person lost the role or is deleted,
// and neither past the server's bound of people it walks (nobody checked, so nothing is said of it).
export type ViewerName = { state: "named"; name: string } | { state: "gone" } | { state: "hidden" };

export function viewerName(v: Viewer): ViewerName {
  if (v.name) return { state: "named", name: v.name };
  if (v.gone) return { state: "gone" };
  return { state: "hidden" };
}

// The faults a comment call can meet, by what the person must be told. Anything else is shown as the common text
// for the code (failureText), never the server's English message.
// "limit" is an item at its comment bound; "author" is an edit by someone who did not write the comment; "reply" is a
// reply put under another reply (the server's reason reply_to_reply).
export type CommentFault = "off" | "role" | "rate" | "gone" | "viewers_off" | "limit" | "author" | "reply" | "other";

export function commentFault(err: unknown): CommentFault {
  if (!(err instanceof ApiError)) return "other";
  if (err.code === "forbidden" && err.details.reason === "comments_off") return "off";
  if (err.code === "forbidden" && err.details.reason === "role") return "role";
  if (err.code === "forbidden" && err.details.reason === "who_viewed_off") return "viewers_off";
  if (err.code === "forbidden" && err.details.reason === "author") return "author";
  if (err.code === "conflict" && err.details.reason === "limit") return "limit";
  if (err.code === "invalid_request" && err.details.reason === "reply_to_reply") return "reply";
  if (err.code === "rate_limited") return "rate";
  if (err.code === "not_found") return "gone";
  return "other";
}

// formatWhen writes a time in the language, or "" for a value that is not a time.
export function formatWhen(iso: string | null | undefined, lang: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" }).format(d);
}
