// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../session";
import { mkdir, mkdirIn } from "./api";

// fu2 (decision 74): a folder made in a directory names its parent by ID, and the root keeps the path form.
describe("making a folder", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    session.set({ user: { id: "usr_a", email: "a@example.test", role: "member" } as never, account: null, csrf: "csrf", publicLinks: false });
    fetchMock = vi.fn(async () => new Response('{"id":"nod_new"}', { status: 201, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    session.set(null);
  });

  async function sentBody(): Promise<unknown> {
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    return JSON.parse(String(init.body));
  }

  it("names the parent directory by its ID, with the new name", async () => {
    await mkdirIn("fld_mine", "nod_parent", "Plans");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/folders/fld_mine/directories");
    expect(await sentBody()).toEqual({ parent: "nod_parent", name: "Plans" });
  });

  it("keeps the path form at a folder's root", async () => {
    await mkdir("fld_mine", "/Plans");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/folders/fld_mine/directories");
    expect(await sentBody()).toEqual({ path: "/Plans", parents: false });
  });
});
