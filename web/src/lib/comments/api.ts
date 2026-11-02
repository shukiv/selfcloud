// SPDX-License-Identifier: AGPL-3.0-or-later

import { api } from "../api";

// The comment, activity and who-viewed routes (design section 5 and decisions 39, 50 and 51). Every ID in a path is
// the server's own; the page never shows one. A name is text: the components render it inside a bdi element.

export interface Comment {
  id: string;
  parent_id?: string;
  deleted?: boolean;
  body?: string;
  mine?: boolean;
  author_name?: string;
  author_gone?: boolean;
  created_at: string;
  edited_at?: string;
  mentions: { name: string }[];
  resolved: boolean;
  resolved_at?: string;
  resolved_by_name?: string;
  resolved_by_gone?: boolean;
  reply_count?: number;
  replies?: Comment[];
  replies_more?: boolean;
}

export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export interface Person {
  ref: string;
  name: string;
}

export interface ActivityActor {
  kind: "user" | "account_key" | "system" | "visitor" | "admin" | "other";
  name?: string;
  gone?: boolean;
  mine?: boolean;
}

export interface ActivityEntry {
  id: string;
  time: string;
  kind: "content" | "comment" | "access" | "label";
  action: string;
  actor: ActivityActor;
  details?: Record<string, unknown>;
}

export interface Viewer {
  user_id: string;
  name?: string;
  gone?: boolean;
  viewed_at: string;
}

// The limit of one page of comments and of activity. The server caps a page at 500 and the panels ask for 20.
export const PAGE = 20;

const q = (params: Record<string, string | number | undefined>): string => {
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== "")
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return parts.length > 0 ? `?${parts.join("&")}` : "";
};

export function listComments(node: string, cursor?: string): Promise<Page<Comment>> {
  return api<Page<Comment>>("GET", `/api/v1/nodes/${encodeURIComponent(node)}/comments${q({ limit: PAGE, cursor })}`);
}

export function listReplies(node: string, parent: string, cursor?: string): Promise<Page<Comment>> {
  return api<Page<Comment>>(
    "GET",
    `/api/v1/nodes/${encodeURIComponent(node)}/comments${q({ parent, limit: PAGE, cursor })}`,
  );
}

export interface NewComment {
  body: string;
  parent_id?: string;
  mentions?: string[];
}

export function createComment(node: string, body: NewComment): Promise<Comment> {
  return api<Comment>("POST", `/api/v1/nodes/${encodeURIComponent(node)}/comments`, { body });
}

export function editComment(id: string, body: string): Promise<Comment> {
  return api<Comment>("PATCH", `/api/v1/comments/${encodeURIComponent(id)}`, { body: { body } });
}

export function deleteComment(id: string): Promise<void> {
  return api<void>("DELETE", `/api/v1/comments/${encodeURIComponent(id)}`);
}

export function resolveComment(id: string, resolved: boolean): Promise<Comment> {
  return api<Comment>("POST", `/api/v1/comments/${encodeURIComponent(id)}/resolve`, { body: { resolved } });
}

// listPeople answers the people a comment may name, at most five, by reference (never by ID).
export function listPeople(node: string, text: string): Promise<{ items: Person[] }> {
  return api<{ items: Person[] }>("GET", `/api/v1/nodes/${encodeURIComponent(node)}/people${q({ q: text })}`);
}

export function listActivity(node: string, cursor?: string): Promise<Page<ActivityEntry>> {
  return api<Page<ActivityEntry>>("GET", `/api/v1/nodes/${encodeURIComponent(node)}/activity${q({ limit: PAGE, cursor })}`);
}

export function listViewers(node: string, cursor?: string): Promise<Page<Viewer>> {
  return api<Page<Viewer>>("GET", `/api/v1/nodes/${encodeURIComponent(node)}/viewers${q({ limit: PAGE, cursor })}`);
}
