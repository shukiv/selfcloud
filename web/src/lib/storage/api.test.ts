// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import { get } from "../store";

const mocks = vi.hoisted(() => ({ api: vi.fn() }));
vi.mock("../api", () => ({ api: mocks.api }));

import { myBreakdown, myLargest, myStorage, refreshMyBreakdown, refreshMyStorage } from "./api";

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

describe("refreshMyBreakdown and myLargest (decision 79)", () => {
  beforeEach(() => {
    mocks.api.mockReset();
    myBreakdown.set(null);
  });

  it("reads GET /me with the breakdown asked for, and keeps the breakdown apart from the meter's figures", async () => {
    mocks.api.mockResolvedValueOnce({
      storage: { used_bytes: 90, reserved_bytes: 0, quota_bytes: 100, available_bytes: 10, breakdown: { versions_bytes: 60, versions_files: 1, trash_bytes: 10, trash_files: 1 } },
    });
    await refreshMyBreakdown();
    expect(mocks.api).toHaveBeenCalledWith("GET", "/api/v1/me?storage=breakdown");
    expect(get(myBreakdown)).toEqual({ versions_bytes: 60, versions_files: 1, trash_bytes: 10, trash_files: 1 });
    expect(get(myStorage)?.used_bytes).toBe(90);
    // A plain refresh of the meter does not clear the breakdown.
    mocks.api.mockResolvedValueOnce({ storage: { used_bytes: 95, reserved_bytes: 0, quota_bytes: 100, available_bytes: 5 } });
    await refreshMyStorage();
    expect(get(myBreakdown)).toEqual({ versions_bytes: 60, versions_files: 1, trash_bytes: 10, trash_files: 1 });
  });

  it("sends the chosen kind of file with the page, and none for every file", async () => {
    mocks.api.mockResolvedValue({ items: [] });
    await myLargest(20, null, "audio");
    expect(mocks.api).toHaveBeenLastCalledWith("GET", "/api/v1/me/storage/largest?kind=audio&limit=20");
    await myLargest(20, "c2", "");
    expect(mocks.api).toHaveBeenLastCalledWith("GET", "/api/v1/me/storage/largest?limit=20&cursor=c2");
  });
});
