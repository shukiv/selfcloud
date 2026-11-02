// SPDX-License-Identifier: AGPL-3.0-or-later

import { api } from "../api";

// The notification routes (design section 5 and decisions 43 and 55). A signed-in person reads only their own. The page
// never shows an ID: a notice's names are text, and its item is opened by its node ID, which is never printed.

export interface Notice {
  id: string;
  kind: string;
  node_id: string;
  item_name: string;
  actor_name?: string;
  actor_gone?: boolean;
  created_at: string;
  read_at?: string;
}

export interface NoticePage {
  items: Notice[];
  next_cursor?: string;
}

// The count of the bell. more is set when the server stopped counting at its bound.
export interface Unread {
  count: number;
  more?: boolean;
}

export const PAGE = 20;

export function listNotices(cursor?: string): Promise<NoticePage> {
  const qs = new URLSearchParams({ limit: String(PAGE) });
  if (cursor) qs.set("cursor", cursor);
  return api<NoticePage>("GET", `/api/v1/notifications?${qs.toString()}`);
}

export function unreadCount(): Promise<Unread> {
  return api<Unread>("GET", "/api/v1/notifications/unread");
}

// markRead marks one notification read. The server answers 204 and changes nothing else.
export function markRead(id: string): Promise<void> {
  return api<void>("POST", `/api/v1/notifications/${encodeURIComponent(id)}/read`);
}

export interface EmailSwitch {
  email: boolean;
}

export function getEmailSwitch(): Promise<EmailSwitch> {
  return api<EmailSwitch>("GET", "/api/v1/notifications/settings");
}

export function setEmailSwitch(email: boolean): Promise<EmailSwitch> {
  return api<EmailSwitch>("PATCH", "/api/v1/notifications/settings", { body: { email } });
}
