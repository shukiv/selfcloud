// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { brand } from "./brand";
import { get } from "./store";
import { refreshBrand } from "./auth";

interface Held {
  resolve: (name: string) => void;
}

// Each read of /api/v1/info is held until the test answers it, so the test decides the order the answers arrive in.
const held: Held[] = [];

beforeEach(() => {
  held.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          held.push({
            resolve: (name) =>
              resolve(new Response(JSON.stringify({ brand: { name, accent: "#053D8E", logo_url: "/brand/logo", artwork: false }, features: {}, password_min_length: 8 }), { status: 200, headers: { "Content-Type": "application/json" } })),
          });
        }),
    ),
  );
  brand.set({ name: "Before", accent: "#053D8E", logo_url: null, artwork: true });
});
afterEach(() => vi.unstubAllGlobals());

describe("refreshBrand", () => {
  it("never lets an older answer land after a newer one", async () => {
    const first = refreshBrand();
    const second = refreshBrand();
    await vi.waitFor(() => expect(held).toHaveLength(2));
    held[1].resolve("Newer");
    await second;
    held[0].resolve("Older");
    await first;
    expect(get(brand).name).toBe("Newer");
  });

  it("applies an answer when no later call has started", async () => {
    const only = refreshBrand();
    await vi.waitFor(() => expect(held).toHaveLength(1));
    held[0].resolve("Only");
    await only;
    expect(get(brand).name).toBe("Only");
  });

  it("stamps the logo's address with the hash it is given, the same on every call", async () => {
    const a = refreshBrand("abc123");
    await vi.waitFor(() => expect(held).toHaveLength(1));
    held[0].resolve("A");
    await a;
    expect(get(brand).logo_url).toBe("/brand/logo?v=abc123");
    const b = refreshBrand("abc123");
    await vi.waitFor(() => expect(held).toHaveLength(2));
    held[1].resolve("B");
    await b;
    expect(get(brand).logo_url).toBe("/brand/logo?v=abc123");
  });

  it("leaves the address as the server gave it when there is no hash", async () => {
    const a = refreshBrand();
    await vi.waitFor(() => expect(held).toHaveLength(1));
    held[0].resolve("A");
    await a;
    expect(get(brand).logo_url).toBe("/brand/logo");
  });
});
