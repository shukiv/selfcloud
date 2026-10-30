// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../session";

const myFolders = vi.hoisted(() => vi.fn(async () => [{ id: "fld_a", name: "Team A", kind: "team", role: "manager" }]));
vi.mock("./api", () => ({ myFolders }));

import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "./folders";

describe("the folder lists are read only inside a session", () => {
  beforeEach(() => {
    myFolders.mockClear();
    session.set(null);
    folders.set([]);
    hiddenFolders.set([]);
  });

  it("sends no request for the folders when there is no session, such as just after a sign-out", async () => {
    await loadFolders();
    await loadHiddenFolders();
    expect(myFolders).not.toHaveBeenCalled();
  });
});
