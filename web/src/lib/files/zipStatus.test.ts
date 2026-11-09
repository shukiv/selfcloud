// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { LANGUAGES, loadLang, translate } from "../i18n";
import { makeZip, parseSkipped, zipErrorText, zipStatus, zipTooManyText, ZIP_MAX_ITEMS, type ZipDeps } from "./zips";
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

// zipFetch answers the zip's first-byte GET with a status, the X-Selfcloud-Skipped value, and the body the server
// sends with it: the JSON error for a refusal, or nothing.
function zipFetch(status: number, opts: { skipped?: string; body?: unknown } = {}) {
  return vi.fn(async () => {
    const headers = new Headers({ "Content-Type": "application/json" });
    if (opts.skipped !== undefined) headers.set("X-Selfcloud-Skipped", opts.skipped);
    return new Response(opts.body === undefined ? null : JSON.stringify(opts.body), { status, headers });
  });
}

// refusal is the server's error body for a code.
const refusal = (code: string) => ({ error: { code, details: {} } });

describe("zipStatus", () => {
  beforeAll(async () => {
    await loadLang("en");
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("asks with a GET of the first byte on the zip's own address, and says not ready on 202", async () => {
    const f = zipFetch(202, { body: { state: "pending" } });
    vi.stubGlobal("fetch", f);
    await expect(zipStatus("zip_1")).resolves.toEqual({ ready: false, skipped: 0 });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/zips/zip_1");
    expect(init.method).toBe("GET");
    expect((init.headers as Record<string, string>).Range).toBe("bytes=0-0");
    expect(init.credentials).toBe("same-origin");
  });

  it("reads the left-out count on 206, and on 200 when the server ignores the range", async () => {
    vi.stubGlobal("fetch", zipFetch(206, { skipped: "4" }));
    await expect(zipStatus("zip_1")).resolves.toEqual({ ready: true, skipped: 4 });
    vi.stubGlobal("fetch", zipFetch(200, { skipped: "2" }));
    await expect(zipStatus("zip_1")).resolves.toEqual({ ready: true, skipped: 2 });
  });

  it("reads a bad count as nothing left out, and still says ready", async () => {
    for (const bad of ["NaN", "-1", "2.5", ""]) {
      vi.stubGlobal("fetch", zipFetch(206, { skipped: bad }));
      await expect(zipStatus("zip_1"), bad).resolves.toEqual({ ready: true, skipped: 0 });
    }
  });

  it("refuses a missing zip as not_found", async () => {
    vi.stubGlobal("fetch", zipFetch(404, { body: refusal("not_found") }));
    await expect(zipStatus("zip_1")).rejects.toMatchObject({ status: 404, code: "not_found" });
  });

  it("answers an error with no JSON body as internal", async () => {
    vi.stubGlobal("fetch", zipFetch(500));
    await expect(zipStatus("zip_1")).rejects.toMatchObject({ status: 500, code: "internal" });
  });

  it("refuses a 403 download_restricted as the download refusal, by its code", async () => {
    vi.stubGlobal("fetch", zipFetch(403, { body: refusal("download_restricted") }));
    const err = await zipStatus("zip_1").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 403, code: "download_restricted" });
    expect(zipErrorText(err, "en")).toBe(translate("en", "files.err.downloadRestricted"));
  });

  it("gives a 403 with another code its own message, not the download refusal", async () => {
    vi.stubGlobal("fetch", zipFetch(403, { body: refusal("user_disabled") }));
    const err = await zipStatus("zip_1").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 403, code: "user_disabled" });
    expect(zipErrorText(err, "en")).toBe(translate("en", "error.userDisabled"));
    expect(zipErrorText(err, "en")).not.toBe(translate("en", "files.err.downloadRestricted"));
  });

  it("shows a suspended account its suspended message", async () => {
    vi.stubGlobal("fetch", zipFetch(403, { body: refusal("account_suspended") }));
    const err = await zipStatus("zip_1").catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 403, code: "account_suspended" });
    expect(zipErrorText(err, "en")).toBe(translate("en", "error.accountSuspended"));
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
