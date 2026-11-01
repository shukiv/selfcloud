// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../session";
import { childrenPage } from "./api";
import { sortNodes } from "./sort";
import type { Node } from "./types";

// The sort is sent to the server with the page (decision 47). Without a sort the request is the one API clients always made.
describe("childrenPage asks the server for the chosen sort", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    session.set({ user: {}, account: null, csrf: "c", publicLinks: false } as unknown as Session);
    fetchMock = vi.fn(async () => new Response('{"items":[],"next_cursor":null}', { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    session.set(null);
    vi.unstubAllGlobals();
  });

  const urlOf = (i: number) => String(fetchMock.mock.calls[i][0]);

  it("sends the sort and the order with a folder's root", async () => {
    await childrenPage("fld_aaaaaaaa", null, undefined, { key: "size", dir: "desc" });
    expect(urlOf(0)).toBe("/api/v1/folders/fld_aaaaaaaa/children?path=%2F&limit=50&sort=size&order=desc");
  });

  it("sends the sort and the cursor with a directory", async () => {
    await childrenPage("fld_aaaaaaaa", "nod_bbbbbbbb", "CUR", { key: "modified", dir: "asc" });
    expect(urlOf(0)).toBe("/api/v1/nodes/nod_bbbbbbbb/children?limit=50&sort=modified&order=asc&cursor=CUR");
  });

  it("sends no sort when none is chosen, as before", async () => {
    await childrenPage("fld_aaaaaaaa", "nod_bbbbbbbb");
    expect(urlOf(0)).toBe("/api/v1/nodes/nod_bbbbbbbb/children?limit=50");
  });
});

// The name order the server gives (internal/names/collate_test.go, TestCompareOrder) is the order the web's collator gave
// the same names before the change. The fixture and the expected order are the same in both tests.
describe("the server's name order is the web's collator order for the fixture", () => {
  it("matches the order pinned in Go", () => {
    const fixture = [
      "file 10", "file 2", "File 1", "Résumé", "resume", "שלום", "ארכיון", "תיק", "בית", "אבא",
      "ם", "מ", "ק", "مستند", "صورة", "كتاب", "كاتب", "a-b", "a b", "ab", "B", "b", "1", "10", "2",
      "a1", "a10", "a2", "_x", "#tag", "zeta", "Ω", "Ζ",
    ];
    const want = [
      "_x", "#tag", "1", "2", "10", "a b", "a-b", "a1", "a2", "a10", "ab", "B", "b", "File 1", "file 2", "file 10",
      "Résumé", "resume", "zeta", "Ζ", "Ω", "אבא", "ארכיון", "בית", "ם", "מ", "ק", "שלום", "תיק",
      "صورة", "كاتب", "كتاب", "مستند",
    ];
    const nodes = fixture.map((name, i) => ({ id: `nod_${i}`, name, kind: "file", size: 0, modified_at: "" }) as unknown as Node);
    const got = sortNodes(nodes, "name", "asc", "und").map((n) => n.name);
    expect(got).toEqual(want);
  });
});
