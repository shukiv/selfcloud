// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { childrenPage } from "./api";
import { ApiError } from "../api";
import { session, type Session } from "../session";

// fu8: the caller's signal reaches the request, so a read the page no longer wants is cancelled.
describe("childrenPage passes the caller's signal to the request (fu8)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    session.set(null);
  });

  it("ends the request with a cancel when the caller aborts it", async () => {
    session.set({ user: { id: "u1", email: "a@example.com", display_name: "A", role: "member", locale: "en", status: "active" }, account: null, csrf: "t", publicLinks: false } as Session);
    let requestSignal: AbortSignal | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: string, init: RequestInit) => {
        requestSignal = init.signal ?? undefined;
        return new Promise((_resolve, reject) => {
          init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        });
      }),
    );
    const ctl = new AbortController();
    const read = childrenPage("fld_a", null, undefined, undefined, ctl.signal);
    ctl.abort();
    const err = await read.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe("cancelled");
    expect(requestSignal?.aborted).toBe(true);
  });
});
