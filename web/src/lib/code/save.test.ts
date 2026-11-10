// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch, fail, type Call } from "../../test/fetchStub";
import { EDIT_LIMIT } from "./edit";
import { readWhole, saveText, type SaveFailure } from "./save";

afterEach(() => {
  vi.unstubAllGlobals();
});

const NODE_ID = "nod_a1";
const OLD_ETAG = '"0123abcd"';
const NEW_ETAG = '"feedbeef"';
const STORED = "application/json; charset=utf-8";

// node is the server's answer to a save: the node with its new ETag.
function node(etag: string, mime = STORED) {
  return { id: NODE_ID, folder_id: "fld_b", parent_id: null, name: "a.json", kind: "file", size: 3, mime_type: mime, etag };
}

const bytes = (n: number) => new Uint8Array(n).fill(0x61);

describe("saveText", () => {
  it("sends the bytes as a PUT with If-Match set to the ETag read, and the stored type unchanged", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: node(NEW_ETAG) }));
    const body = new TextEncoder().encode("{}");
    await saveText(NODE_ID, OLD_ETAG, STORED, body);
    expect(calls).toHaveLength(1);
    const c = calls[0] as Call & { headers?: Record<string, string> };
    expect(c.method).toBe("PUT");
    expect(c.url).toBe(`/api/v1/nodes/${NODE_ID}/content`);
    expect(lastInit().headers).toMatchObject({ "If-Match": OLD_ETAG, "Content-Type": STORED });
    expect(lastInit().body).toBe(body);
  });

  it("sends the stored type exactly, parameters included, and never a type of its own", async () => {
    stubFetch(() => ({ status: 200, body: node(NEW_ETAG, "text/plain") }));
    await saveText(NODE_ID, OLD_ETAG, "text/plain", bytes(1));
    expect(lastInit().headers).toMatchObject({ "Content-Type": "text/plain" });
  });

  it("answers with the node the server sent, whose ETag is the next If-Match", async () => {
    stubFetch(() => ({ status: 200, body: node(NEW_ETAG) }));
    const r = await saveText(NODE_ID, OLD_ETAG, STORED, bytes(1));
    expect(r).toEqual({ ok: true, node: node(NEW_ETAG) });
  });

  it("refuses a body over 2 MiB before any request is made, and lets one at the limit through", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: node(NEW_ETAG) }));
    const over = await saveText(NODE_ID, OLD_ETAG, STORED, bytes(EDIT_LIMIT + 1));
    expect(over).toEqual({ ok: false, why: "tooLarge" });
    expect(calls).toHaveLength(0);
    const at = await saveText(NODE_ID, OLD_ETAG, STORED, bytes(EDIT_LIMIT));
    expect(at.ok).toBe(true);
    expect(calls).toHaveLength(1);
  });

  it.each<[number, SaveFailure]>([
    [412, "conflict"],
    [409, "locked"],
    [403, "forbidden"],
    [404, "gone"],
    [400, "notText"],
    [413, "tooLarge"],
    [500, "failed"],
    // A sign-in that has ended is not a failure to retry (MED-4); a full store has its own notice (LOW-3).
    [401, "signedOut"],
    [507, "storageFull"],
  ])("maps a %i answer to %s", async (status, why) => {
    stubFetch(() => fail(status, "some_code"));
    expect(await saveText(NODE_ID, OLD_ETAG, STORED, bytes(1))).toEqual({ ok: false, why });
  });

  it("maps a request that never gets an answer to failed, and shows no server text", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      throw new TypeError("network down: secret detail");
    }));
    expect(await saveText(NODE_ID, OLD_ETAG, STORED, bytes(1))).toEqual({ ok: false, why: "failed" });
  });

  it("maps an answer that is not the node to failed", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>", { status: 200 })));
    expect(await saveText(NODE_ID, OLD_ETAG, STORED, bytes(1))).toEqual({ ok: false, why: "failed" });
  });

  it("refuses an ETag that is not a quoted tag before sending, as the server would", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: node(NEW_ETAG) }));
    expect(await saveText(NODE_ID, "bare", STORED, bytes(1))).toEqual({ ok: false, why: "failed" });
    expect(calls).toHaveLength(0);
  });
});

describe("readWhole", () => {
  it("reads the whole file with no Range header and keeps its ETag", async () => {
    const f = vi.fn(async () => new Response("hello", { status: 200, headers: { ETag: OLD_ETAG } }));
    vi.stubGlobal("fetch", f);
    const got = await readWhole(NODE_ID);
    expect(got.etag).toBe(OLD_ETAG);
    expect(new TextDecoder().decode(got.bytes)).toBe("hello");
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`/api/v1/nodes/${NODE_ID}/content?disposition=inline`);
    expect(init.method ?? "GET").toBe("GET");
    expect((init.headers as Record<string, string> | undefined)?.Range).toBeUndefined();
  });

  it("refuses a file over 2 MiB, and stops reading it at the limit", async () => {
    let pulled = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        pulled++;
        c.enqueue(new Uint8Array(64 * 1024));
      },
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(stream, { status: 200, headers: { ETag: OLD_ETAG } })));
    await expect(readWhole(NODE_ID)).rejects.toMatchObject({ status: 413 });
    expect(pulled).toBeLessThan(64);
  });

  it("gives the status of a refusal, so the page shows its own message", async () => {
    stubFetch(() => fail(403, "download_restricted"));
    await expect(readWhole(NODE_ID)).rejects.toMatchObject({ status: 403 });
  });

  it("refuses an answer with no ETag: a save could not be checked against it", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("x", { status: 200 })));
    await expect(readWhole(NODE_ID)).rejects.toMatchObject({ status: 500 });
  });
});

describe("saveText's timeout", () => {
  it("gives up on a save with no answer after 60 s: the request is aborted, and the save is failed (Retry)", async () => {
    vi.useFakeTimers();
    try {
      const fetchMock = vi.fn(
        (_url: string, init: RequestInit) =>
          new Promise<Response>((_, reject) => {
            init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
          }),
      );
      vi.stubGlobal("fetch", fetchMock);
      const pending = saveText(NODE_ID, OLD_ETAG, STORED, bytes(1));
      const signal = (fetchMock.mock.calls[0]?.[1] as RequestInit | undefined)?.signal;
      expect(signal).toBeDefined();
      await vi.advanceTimersByTimeAsync(59_999);
      expect(signal?.aborted).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      expect(signal?.aborted).toBe(true);
      expect(await pending).toEqual({ ok: false, why: "failed" });
    } finally {
      vi.useRealTimers();
    }
  });
});

// lastInit is the request init of the last fetch call made in this test.
function lastInit(): RequestInit & { headers?: Record<string, string> } {
  const f = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
  const call = f.mock.calls.at(-1) as unknown as [string, RequestInit] | undefined;
  return (call?.[1] ?? {}) as RequestInit & { headers?: Record<string, string> };
}
