// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist reads a folder's later pages with the listing's sort: a cursor made under one sort is refused under another.

import { describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ childrenPage: vi.fn(async () => ({ items: [], next_cursor: null })) }));
vi.mock("./api", async (orig) => ({ ...(await orig<typeof import("./api")>()), ...api }));

import { folderPageReader } from "./folderPages";

describe("folderPageReader", () => {
  it("asks for each later page with the listing's sort", async () => {
    const sort = { key: "size" as const, dir: "desc" as const };
    const read = folderPageReader("fld_1", "nod_dir", sort);
    await read("cursor-2");
    expect(api.childrenPage).toHaveBeenCalledWith("fld_1", "nod_dir", "cursor-2", sort);
  });
});
