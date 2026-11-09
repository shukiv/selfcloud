// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../apiError";
import type { Node } from "../files/types";
import { readCode, readHead } from "./read";

afterEach(() => {
  vi.unstubAllGlobals();
});

const MiB = 1024 * 1024;

// body is an answer's body of total bytes of "a", given 64 KiB at a time and only when asked, which counts what was taken
// from it and whether the reader cancelled the rest.
function body(total: number) {
  const seen = { pulled: 0, cancelled: false };
  const stream = new ReadableStream<Uint8Array>(
    {
      pull(c) {
        if (seen.pulled >= total) {
          c.close();
          return;
        }
        const n = Math.min(64 * 1024, total - seen.pulled);
        seen.pulled += n;
        c.enqueue(new Uint8Array(n).fill(0x61));
      },
      cancel() {
        seen.cancelled = true;
      },
    },
    { highWaterMark: 0 },
  );
  return { stream, seen };
}

function file(name: string, mime: string, size: number): Node {
  return {
    id: `n-${name}`,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
  };
}

const answer = (b: BodyInit | null, status: number) => vi.fn(async () => new Response(b, { status }));

describe("readHead", () => {
  it("refuses a 200 answer to its Range request, and does not read the body it brings", async () => {
    // A proxy that drops the Range header answers with the whole file: a 5 GB log would be read into memory.
    const { stream, seen } = body(3 * MiB);
    vi.stubGlobal("fetch", answer(stream, 200));
    await expect(readHead("n-big.log", 8192)).rejects.toBeInstanceOf(ApiError);
    expect(seen.pulled).toBeLessThan(3 * MiB);
  });

  it("reads no more than it asked for from a 206 answer that sends more, and cancels the rest", async () => {
    const { stream, seen } = body(3 * MiB);
    vi.stubGlobal("fetch", answer(stream, 206));
    const got = await readHead("n-big.log", 8192);
    expect(got).toBeInstanceOf(Uint8Array);
    expect((got as Uint8Array).length).toBe(8192);
    expect(seen.cancelled).toBe(true);
    expect(seen.pulled).toBeLessThanOrEqual(128 * 1024);
  });

  it("gives what a short 206 answer holds", async () => {
    vi.stubGlobal("fetch", answer(new Uint8Array([0x61, 0x62]), 206));
    expect(await readHead("n-a.txt", 8192)).toEqual(new Uint8Array([0x61, 0x62]));
  });

  it("gives restricted for a refused read (403), and no bytes for an empty file (416)", async () => {
    vi.stubGlobal("fetch", answer("", 403));
    expect(await readHead("n-a.txt", 8192)).toBe("restricted");
    vi.stubGlobal("fetch", answer("", 416));
    expect(await readHead("n-a.txt", 8192)).toEqual(new Uint8Array(0));
  });

  it("shows no text of a large file when the server answers 200 to the sniff", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(body(3 * MiB).stream, { status: 200 })),
    );
    await expect(readCode(file("big.log", "text/plain", 5_000_000_000))).rejects.toBeInstanceOf(ApiError);
  });
});

describe("readCode", () => {
  it("says a file is not UTF-8 when its bytes are not, and still gives its text with U+FFFD for each bad sequence", async () => {
    // Latin-1 "café" and a line break.
    vi.stubGlobal("fetch", answer(new Uint8Array([0x63, 0x61, 0x66, 0xe9, 0x0a]), 206));
    const got = await readCode(file("notes.txt", "text/plain", 5));
    expect(got).toMatchObject({ text: "caf�\n", utf8: false, viewer: { kind: "code", pack: "text" } });
  });

  it("says UTF-8 text is UTF-8", async () => {
    vi.stubGlobal("fetch", answer(new TextEncoder().encode("café\n"), 206));
    expect(await readCode(file("notes.txt", "text/plain", 6))).toMatchObject({ text: "café\n", utf8: true });
  });

  it("gives an empty file as empty UTF-8 text with no read", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    expect(await readCode(file("empty.txt", "text/plain", 0))).toMatchObject({ text: "", utf8: true });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
