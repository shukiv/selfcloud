// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMock = vi.hoisted(() => ({ api: vi.fn() }));
vi.mock("../api", () => ({ api: apiMock.api }));

import {
  getTemplatesFolder,
  listAllTemplates,
  listTemplatesPage,
  setTemplatesFolder,
  templateThumbURL,
} from "./templates";

beforeEach(() => apiMock.api.mockReset());

describe("templateThumbURL", () => {
  it("encodes the id as a path segment, so an id with a colon still loads", () => {
    expect(templateThumbURL("builtin:letter")).toBe("/api/v1/templates/builtin%3Aletter/thumbnail");
    expect(templateThumbURL("builtin:lo-letter-serif")).toBe("/api/v1/templates/builtin%3Alo-letter-serif/thumbnail");
  });
});

describe("the listing", () => {
  it("asks for one kind, with the cursor when there is one", async () => {
    apiMock.api.mockResolvedValueOnce({ items: [], next_cursor: "" });
    await listTemplatesPage("spreadsheet");
    expect(apiMock.api).toHaveBeenLastCalledWith("GET", "/api/v1/templates?kind=spreadsheet");
    apiMock.api.mockResolvedValueOnce({ items: [], next_cursor: "" });
    await listTemplatesPage("document", "cur_1");
    expect(apiMock.api).toHaveBeenLastCalledWith("GET", "/api/v1/templates?kind=document&after=cur_1");
  });

  it("follows the cursors until the listing ends", async () => {
    apiMock.api
      .mockResolvedValueOnce({ items: [{ id: "builtin:letter" }], next_cursor: "c1" })
      .mockResolvedValueOnce({ items: [{ id: "nod_abcdefgh" }], next_cursor: "" });
    const all = await listAllTemplates("document");
    expect(all.map((i) => i.id)).toEqual(["builtin:letter", "nod_abcdefgh"]);
    expect(apiMock.api).toHaveBeenCalledTimes(2);
  });
});

describe("the account's templates folder", () => {
  it("reads the setting with GET, as its node ID or null", async () => {
    apiMock.api.mockResolvedValueOnce({ node_id: "nod_abcdefgh" });
    expect(await getTemplatesFolder("acc_abcdefgh")).toBe("nod_abcdefgh");
    expect(apiMock.api).toHaveBeenLastCalledWith("GET", "/api/v1/accounts/acc_abcdefgh/templates");
  });

  it("saves a directory with PUT, and null clears it", async () => {
    apiMock.api.mockResolvedValueOnce({ node_id: "nod_abcdefgh" });
    await setTemplatesFolder("acc_abcdefgh", "nod_abcdefgh");
    expect(apiMock.api).toHaveBeenLastCalledWith("PUT", "/api/v1/accounts/acc_abcdefgh/templates", { body: { node_id: "nod_abcdefgh" } });
    apiMock.api.mockResolvedValueOnce({ node_id: null });
    await setTemplatesFolder("acc_abcdefgh", null);
    expect(apiMock.api).toHaveBeenLastCalledWith("PUT", "/api/v1/accounts/acc_abcdefgh/templates", { body: { node_id: null } });
  });
});
