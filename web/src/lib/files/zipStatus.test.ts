// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { LANGUAGES, loadLang, translate } from "../i18n";
import { makeZip, parseSkipped, zipStatus, zipTooManyText, ZIP_MAX_ITEMS, type ZipDeps } from "./zips";
import type { Job } from "./types";

describe("parseSkipped", () => {
  it("reads a whole, non-negative count", () => {
    expect(parseSkipped("0")).toBe(0);
    expect(parseSkipped("3")).toBe(3);
    expect(parseSkipped(" 12 ")).toBe(12);
  });

  it("reads anything else as nothing left out", () => {
    for (const bad of [null, "", "NaN", "-2", "1.5", "1e3", "3 items", "Infinity", "+4"]) {
      expect(parseSkipped(bad), String(bad)).toBe(0);
    }
  });
});

// headers makes a HEAD answer with the given status and X-Selfcloud-Skipped value.
function headFetch(status: number, skipped?: string) {
  return vi.fn(async () => {
    const headers = new Headers();
    if (skipped !== undefined) headers.set("X-Selfcloud-Skipped", skipped);
    return new Response(null, { status, headers });
  });
}

describe("zipStatus", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("asks with HEAD on the zip's own address, and says not ready on 202", async () => {
    const f = headFetch(202);
    vi.stubGlobal("fetch", f);
    await expect(zipStatus("zip_1")).resolves.toEqual({ ready: false, skipped: 0 });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/zips/zip_1");
    expect(init.method).toBe("HEAD");
    expect(init.credentials).toBe("same-origin");
  });

  it("reads the left-out count on 200", async () => {
    vi.stubGlobal("fetch", headFetch(200, "4"));
    await expect(zipStatus("zip_1")).resolves.toEqual({ ready: true, skipped: 4 });
  });

  it("reads a bad count as nothing left out, and still says ready", async () => {
    for (const bad of ["NaN", "-1", "2.5", ""]) {
      vi.stubGlobal("fetch", headFetch(200, bad));
      await expect(zipStatus("zip_1"), bad).resolves.toEqual({ ready: true, skipped: 0 });
    }
  });

  it("refuses a missing zip as not_found, and any other answer as internal", async () => {
    vi.stubGlobal("fetch", headFetch(404));
    await expect(zipStatus("zip_1")).rejects.toMatchObject({ status: 404, code: "not_found" });
    vi.stubGlobal("fetch", headFetch(500));
    await expect(zipStatus("zip_1")).rejects.toMatchObject({ status: 500, code: "internal" });
  });
});

describe("the selection limit", () => {
  beforeAll(async () => {
    await Promise.all(LANGUAGES.map((l) => loadLang(l)));
  });
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("is the server's root limit", () => {
    expect(ZIP_MAX_ITEMS).toBe(1000);
  });

  it("names the limit in every language, with its number", () => {
    for (const l of LANGUAGES) {
      const text = zipTooManyText(l);
      // The number is written as the language writes it: 1,000 in English, and in the Arabic digits in Arabic.
      expect(text, l).toContain(new Intl.NumberFormat(l).format(ZIP_MAX_ITEMS));
      expect(text, l).not.toBe(translate(l, "files.zip.invalid"));
    }
  });

  it("refuses more items than that without asking the server", async () => {
    const create = vi.fn(async () => ({}) as Job);
    const d: ZipDeps = {
      create,
      job: vi.fn(),
      status: vi.fn(),
      download: vi.fn(),
      poll: vi.fn() as unknown as ZipDeps["poll"],
    };
    const nodes = Array.from({ length: ZIP_MAX_ITEMS + 1 }, (_, i) => `nod_${i}`);
    const onFailed = vi.fn();
    makeZip(nodes, { lang: "en", onReady: vi.fn(), onFailed, onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(create).not.toHaveBeenCalled();
    expect(onFailed).toHaveBeenCalledWith(zipTooManyText("en"));
  });

  it("the limit itself is allowed", async () => {
    const create = vi.fn(async () => {
      throw new ApiError(500, "internal", {}, 0);
    });
    const d: ZipDeps = { create, job: vi.fn(), status: vi.fn(), download: vi.fn(), poll: vi.fn() as unknown as ZipDeps["poll"] };
    const nodes = Array.from({ length: ZIP_MAX_ITEMS }, (_, i) => `nod_${i}`);
    makeZip(nodes, { lang: "en", onReady: vi.fn(), onFailed: vi.fn(), onGiveUp: vi.fn() }, d);
    await vi.advanceTimersByTimeAsync(0);
    expect(create).toHaveBeenCalledTimes(1);
  });
});
