// SPDX-License-Identifier: AGPL-3.0-or-later

import { validID } from "../files/route";
import type { Notice } from "./api";

export type { Notice } from "./api";

// The bell reads the count once a minute while the tab is shown. Hidden, it does not read at all (false stops the timer);
// when the tab comes back the read happens at once, as react-query's window focus read does.
export const POLL_MS = 60_000;
export function pollInterval(hidden: boolean): number | false {
  return hidden ? false : POLL_MS;
}

// The count the bell and its screen-reader text show. The server reads at most 100 rows, so a count of 100 or more
// is shown as 99+. Zero is a sentence of its own, so a screen reader hears the state and not only a number. The
// parameter is n, not count: i18next reads count as a plural selector.
export interface Text {
  key: string;
  params?: Record<string, string>;
}
export function unreadState(count: number, more: boolean): Text {
  if (count === 0 && !more) return { key: "notifications.noneUnread" };
  return { key: "notifications.unreadCount", params: { n: badgeText(count, more) ?? "99+" } };
}

// badgeText is the number the badge, the button's name and the live region all show, so they agree. When the server
// stopped counting (more), the number is 99+ whatever the reachable count is: there are at least a hundred unread
// rows, and this count is only the ones the person reaches among the newest of them. null means no badge.
export function badgeText(count: number, more: boolean): string | null {
  if (count === 0 && !more) return null;
  return count > 99 || more ? "99+" : String(count);
}

// The sentence key of a kind the page knows. A kind it does not know gets the generic sentence, never the kind itself.
export function sentenceKey(kind: string): string {
  if (kind === "mention") return "notifications.mention";
  if (kind === "reply") return "notifications.reply";
  return "notifications.generic";
}

type T = (key: string, params?: Record<string, string>) => string;

// actorText is the name of the person who acted. A person who is gone reads as a former member; a name the server does
// not give reads as someone, which is not the same as gone.
export function actorText(n: Notice, t: T): string {
  if (n.actor_gone) return t("notifications.former");
  if (!n.actor_name) return t("notifications.someone");
  return n.actor_name;
}

// itemText is the name of the file. An item without a name reads as an item.
export function itemText(n: Notice, t: T): string {
  return n.item_name ? n.item_name : t("notifications.unnamed");
}

// noticeSentence is the line a notification shows: who did what, on which file. The names are text, never IDs.
export function noticeSentence(n: Notice, t: T): string {
  return t(sentenceKey(n.kind), { actor: actorText(n, t), item: itemText(n, t) });
}

// detailsPath is the address that opens the file with its comments (the /n/ address, with the request).
export function detailsPath(nodeID: string): string {
  return `/n/${nodeID}?details=comments`;
}

// nextPage is the cursor of the page after last, while the server gives one.
export function nextPage(last: { items?: unknown[]; next_cursor?: string }): string | undefined {
  return last.next_cursor ? last.next_cursor : undefined;
}

// followEmpty reads a page, and on through pages that come back empty while the server still gives a cursor (the server
// reads a window of its rows, and the reach filter may leave a window empty). The rounds are bounded.
export const MAX_EMPTY_ROUNDS = 20;
export async function followEmpty<T>(
  fetchPage: (cursor?: string) => Promise<{ items: T[]; next_cursor?: string }>,
  cursor?: string,
): Promise<{ items: T[]; next_cursor?: string }> {
  let page = await fetchPage(cursor);
  let n = 0;
  while (page.items.length === 0 && page.next_cursor && n < MAX_EMPTY_ROUNDS) {
    page = await fetchPage(page.next_cursor);
    n++;
  }
  return page;
}

// formatWhen is the local date and time of an ISO time, in the language, or "" for a time that does not read.
export function formatWhen(iso: string | null | undefined, lang: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" }).format(d);
}

// validNotice checks the two IDs a notification carries before either is used in an address or a request. The server's
// own shape is checked here too, so a bad answer never reaches a path.
export function validNotice(n: Notice): boolean {
  return validID("nod", n.node_id) && /^ntf_[a-z0-9]{8,64}$/.test(n.id);
}
