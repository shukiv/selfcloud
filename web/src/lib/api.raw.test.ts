// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

afterEach(() => vi.unstubAllGlobals());

function answer(status: number, body: unknown) {
  const fn = vi.fn(async (_url: string, _init: RequestInit = {}) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

describe("a raw body", () => {
  it("is sent as it is, with its own content type, and not as JSON", async () => {
    const fn = answer(200, { ok: true });
    const file = new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" });
    await api("PUT", "/api/v1/settings/brand/logo", { raw: { body: file, type: "image/png" } });
    const init = fn.mock.calls[0][1];
    expect(init.method).toBe("PUT");
    expect(init.body).toBe(file);
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("image/png");
  });

  it("a JSON body is still JSON", async () => {
    const fn = answer(200, {});
    await api("PATCH", "/api/v1/settings", { body: { reset: ["a"] } });
    const init = fn.mock.calls[0][1];
    expect(init.body).toBe('{"reset":["a"]}');
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });
});

describe("a refused request", () => {
  it("keeps the message the server gave, apart from the code the page acts on", async () => {
    answer(400, { error: { code: "invalid_request", message: "the logo is not an SVG file", details: { reason: "invalid_picture" } } });
    const err = await api("PUT", "/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("invalid_request");
    expect((err as ApiError).serverMessage).toBe("the logo is not an SVG file");
    expect((err as ApiError).message).toBe("invalid_request");
  });

  it("has no message when the answer gave none", async () => {
    answer(500, { error: { code: "internal" } });
    const err = await api("GET", "/x").catch((e: unknown) => e);
    expect((err as ApiError).serverMessage).toBe("");
  });
});
