// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import {
  POLL_MS,
  badgeText,
  detailsPath,
  followEmpty,
  itemText,
  nextPage,
  noticeSentence,
  pollInterval,
  sentenceKey,
  unreadState,
  validNotice,
  type Notice,
} from "./logic";

// A translator that shows the key and its parameters, so a test reads what was asked for.
const t = (key: string, params?: Record<string, string>) => (params ? `${key} ${JSON.stringify(params)}` : key);

const base: Notice = {
  id: "ntf_abcdefgh",
  kind: "mention",
  node_id: "nod_abcdefgh",
  item_name: "plan.txt",
  actor_name: "Ann",
  created_at: "2026-11-01T10:00:00Z",
};

describe("the bell's polling", () => {
  it("reads every minute while the tab is shown", () => {
    expect(POLL_MS).toBe(60_000);
    expect(pollInterval(false)).toBe(60_000);
  });

  it("does not read while the tab is hidden", () => {
    expect(pollInterval(true)).toBe(false);
  });
});

describe("the unread count", () => {
  it("says none when nothing is unread, and the count otherwise", () => {
    expect(unreadState(0, false)).toEqual({ key: "notifications.noneUnread", params: undefined });
    expect(unreadState(3, false)).toEqual({ key: "notifications.unreadCount", params: { n: "3" } });
  });

  it("shows 99+ with a badge when the server stopped counting, even with no reachable count", () => {
    expect(unreadState(0, true)).toEqual({ key: "notifications.unreadCount", params: { n: "99+" } });
    expect(badgeText(0, true)).toBe("99+");
    expect(badgeText(0, false)).toBeNull();
    expect(badgeText(4, false)).toBe("4");
  });

  it("shows 99+ for any count over 99", () => {
    expect(unreadState(100, true)).toEqual({ key: "notifications.unreadCount", params: { n: "99+" } });
    expect(unreadState(150, false)).toEqual({ key: "notifications.unreadCount", params: { n: "99+" } });
  });
});

describe("the sentence of a notification", () => {
  it("maps the two kinds the server sends", () => {
    expect(sentenceKey("mention")).toBe("notifications.mention");
    expect(sentenceKey("reply")).toBe("notifications.reply");
  });

  it("gives a mention the actor and the item", () => {
    expect(noticeSentence(base, t)).toBe('notifications.mention {"actor":"Ann","item":"plan.txt"}');
  });

  it("names a former member, and never a raw ID, when the actor is gone", () => {
    const gone = { ...base, kind: "reply", actor_name: undefined, actor_gone: true };
    expect(noticeSentence(gone, t)).toBe('notifications.reply {"actor":"notifications.former","item":"plan.txt"}');
  });

  it("says someone when the server gives no name, and does not say the actor is gone", () => {
    const hidden = { ...base, actor_name: undefined };
    expect(noticeSentence(hidden, t)).toBe('notifications.mention {"actor":"notifications.someone","item":"plan.txt"}');
  });

  it("uses a generic sentence for a kind this page does not know, never the kind itself", () => {
    const other = { ...base, kind: "share" };
    expect(noticeSentence(other, t)).toBe('notifications.generic {"actor":"Ann","item":"plan.txt"}');
  });

  it("names an item with no name as an item", () => {
    expect(itemText({ ...base, item_name: "" }, t)).toBe("notifications.unnamed");
    expect(itemText(base, t)).toBe("plan.txt");
  });
});

describe("the paging of the list", () => {
  it("reads on through an empty page that has a cursor, to the first page with notices", async () => {
    const pages: Record<string, { items: string[]; next_cursor?: string }> = {
      "": { items: [], next_cursor: "a" },
      a: { items: [], next_cursor: "b" },
      b: { items: ["x"], next_cursor: "c" },
    };
    const page = await followEmpty(async (c) => pages[c ?? ""]!);
    expect(page).toEqual({ items: ["x"], next_cursor: "c" });
  });

  it("stops at an empty page without a cursor", async () => {
    const page = await followEmpty(async () => ({ items: [] as string[] }));
    expect(page).toEqual({ items: [] });
  });

  it("gives the cursor of the next page only while the server gives one", () => {
    expect(nextPage({ items: [], next_cursor: "c1" })).toBe("c1");
    expect(nextPage({ items: [] })).toBeUndefined();
  });
});

describe("the address of a notification", () => {
  it("opens the file with its comments", () => {
    expect(detailsPath("nod_abcdefgh")).toBe("/n/nod_abcdefgh?details=comments");
  });

  it("accepts a notice whose IDs have the server's shape", () => {
    expect(validNotice(base)).toBe(true);
  });

  it.each([
    ["a node ID with a path in it", { node_id: "nod_../../x" }],
    ["a node ID of another kind", { node_id: "usr_abcdefgh" }],
    ["a notification ID with a space", { id: "ntf_abc defgh" }],
    ["an empty notification ID", { id: "" }],
  ])("refuses %s", (_, over) => {
    expect(validNotice({ ...base, ...over })).toBe(false);
  });
});
