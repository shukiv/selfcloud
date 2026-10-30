// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import { get } from "./store";
import { session, type Session } from "./session";

const mocks = vi.hoisted(() => ({ myFolders: vi.fn(), api: vi.fn(), listAccessRequests: vi.fn() }));
vi.mock("./files/api", () => ({ myFolders: mocks.myFolders }));
vi.mock("./sharing/api", () => ({ listAccessRequests: mocks.listAccessRequests }));
vi.mock("./api", () => ({ api: mocks.api }));

import { folders, hiddenFolders, loadFolders, loadHiddenFolders } from "./files/folders";
import { myStorage, refreshMyStorage } from "./storage/api";
import { pendingRequests, refreshPendingRequests } from "./sharing/requestCount";
import { ApiError } from "./apiError";

function person(id: string): Session {
  return { user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en" }, account: null, csrf: id, publicLinks: false } as unknown as Session;
}

// held returns a promise to answer by hand, so the session can change while a request is out.
function held<T>() {
  let settle!: { resolve: (v: T) => void; reject: (e: unknown) => void };
  const promise = new Promise<T>((resolve, reject) => {
    settle = { resolve, reject };
  });
  return { promise, ...settle };
}

// An answer that arrives after the session changed belongs to the person who asked. It must not fill the
// next person's stores: the reset at the change of session has already run, and nothing would empty them again.
describe("a late answer never fills the stores of the next session", () => {
  beforeEach(() => {
    session.set(person("ann"));
    folders.set([]);
    hiddenFolders.set([]);
    myStorage.set(null);
    pendingRequests.set(null);
  });

  it("drops the folder list", async () => {
    const answer = held<unknown[]>();
    mocks.myFolders.mockReturnValue(answer.promise);
    const load = loadFolders();
    session.set(person("bob"));
    answer.resolve([{ id: "fld_a", name: "Ann's private team" }]);
    await expect(load).resolves.toEqual([]);
    expect(get(folders)).toEqual([]);
  });

  it("drops the hidden folder list", async () => {
    const answer = held<unknown[]>();
    mocks.myFolders.mockReturnValue(answer.promise);
    const load = loadHiddenFolders();
    session.set(null);
    answer.resolve([{ id: "fld_a", name: "Ann's hidden team" }]);
    await expect(load).resolves.toEqual([]);
    expect(get(hiddenFolders)).toEqual([]);
  });

  it("drops the storage figures", async () => {
    const answer = held<{ storage: unknown }>();
    mocks.api.mockReturnValue(answer.promise);
    const load = refreshMyStorage();
    session.set(person("bob"));
    answer.resolve({ storage: { used_bytes: 7 } });
    await load;
    expect(get(myStorage)).toBeNull();
  });

  it("drops the request count, and a late failure does not touch the next person's count", async () => {
    const answer = held<{ items: unknown[] }>();
    mocks.listAccessRequests.mockReturnValue(answer.promise);
    const load = refreshPendingRequests();
    session.set(person("bob"));
    answer.resolve({ items: [{}, {}] });
    await expect(load).resolves.toBeNull();
    expect(get(pendingRequests)).toBeNull();

    const failing = held<{ items: unknown[] }>();
    mocks.listAccessRequests.mockReturnValue(failing.promise);
    const failed = refreshPendingRequests();
    session.set(person("cy"));
    pendingRequests.set(3);
    failing.reject(new ApiError(500, "internal", {}, 0));
    await failed;
    expect(get(pendingRequests)).toBe(3);
  });

  it("keeps an answer that arrives in the same session", async () => {
    mocks.myFolders.mockResolvedValue([{ id: "fld_a", name: "Team A" }]);
    await expect(loadFolders()).resolves.toHaveLength(1);
    expect(get(folders)).toHaveLength(1);
  });
});
