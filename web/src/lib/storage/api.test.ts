// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import { get } from "../store";

const mocks = vi.hoisted(() => ({ api: vi.fn() }));
vi.mock("../api", () => ({ api: mocks.api }));

import { myStorage, refreshMyStorage } from "./api";

// held returns a promise to answer by hand.
function held<T>() {
  let settle!: { resolve: (v: T) => void; reject: (e: unknown) => void };
  const promise = new Promise<T>((resolve, reject) => {
    settle = { resolve, reject };
  });
  return { promise, ...settle };
}

const storage = (used: number) => ({ storage: { used_bytes: used, reserved_bytes: 0, quota_bytes: null, available_bytes: null } });

describe("refreshMyStorage", () => {
  beforeEach(() => {
    mocks.api.mockReset();
    myStorage.set(null);
  });

  it("keeps the newer answer when an older request resolves last", async () => {
    const older = held<unknown>();
    const newer = held<unknown>();
    mocks.api.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);

    const first = refreshMyStorage();
    const second = refreshMyStorage();

    newer.resolve(storage(200));
    await second;
    expect(get(myStorage)?.used_bytes).toBe(200);

    older.resolve(storage(100));
    await first;
    expect(get(myStorage)?.used_bytes).toBe(200);
  });

  it("keeps the figures of the last request when it is the one that resolves last", async () => {
    const older = held<unknown>();
    const newer = held<unknown>();
    mocks.api.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);

    const first = refreshMyStorage();
    const second = refreshMyStorage();

    older.resolve(storage(100));
    await first;
    newer.resolve(storage(200));
    await second;
    expect(get(myStorage)?.used_bytes).toBe(200);
  });
});
