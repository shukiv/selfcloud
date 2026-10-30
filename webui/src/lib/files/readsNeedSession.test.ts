// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../session";
import { childrenPage, statNode } from "./api";

describe("the file reads send nothing without a session", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    session.set(null);
    fetchMock = vi.fn(async () => new Response('{"error":{"code":"unauthorized"}}', { status: 401, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("a page of children is not requested once the session has ended", async () => {
    await expect(childrenPage("fld_aaaaaaaa", null)).rejects.toBeDefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("a node is not requested once the session has ended", async () => {
    await expect(statNode("nod_aaaaaaaa")).rejects.toBeDefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
