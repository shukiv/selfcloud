// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame the More menu saves: its name (the file's own without its last extension, the place floored to m-ss or
// h-mm-ss, .png, at most 255 UTF-8 bytes cut on a code point), and its capture on a canvas of the video's own size,
// never in the document. A video with no picture yet is not_ready; a canvas the browser will not read is unreadable.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FrameError, captureFrame, frameName } from "./frame";

const bytes = (s: string) => new TextEncoder().encode(s).length;

describe("frameName", () => {
  it("takes the name without its last extension, a space, the floored place and .png", () => {
    expect(frameName("mp1-clip.webm", 1.2)).toBe("mp1-clip 0-01.png");
    expect(frameName("a.b.webm", 0)).toBe("a.b 0-00.png");
  });

  it("keeps a name with no extension whole, and one whose only dot is its first character", () => {
    expect(frameName("clip", 65)).toBe("clip 1-05.png");
    expect(frameName(".hidden", 5)).toBe(".hidden 0-05.png");
  });

  it("writes the place floored: 0-00 at zero, m-ss under an hour, h-mm-ss from an hour", () => {
    expect(frameName("v.webm", 0)).toBe("v 0-00.png");
    expect(frameName("v.webm", 75.9)).toBe("v 1-15.png");
    expect(frameName("v.webm", 3725)).toBe("v 1-02-05.png");
    expect(frameName("v.webm", 3600)).toBe("v 1-00-00.png");
    expect(frameName("v.webm", 59.99)).toBe("v 0-59.png");
  });

  it("writes 0-00 for a place that is NaN, Infinity or negative", () => {
    expect(frameName("v.webm", NaN)).toBe("v 0-00.png");
    expect(frameName("v.webm", Infinity)).toBe("v 0-00.png");
    expect(frameName("v.webm", -3)).toBe("v 0-00.png");
  });

  it("cuts a long Hebrew name to 255 bytes on a code point, keeping the time and .png", () => {
    const name = frameName(`${"א".repeat(200)}.webm`, 75.9);
    expect(bytes(name)).toBeLessThanOrEqual(255);
    expect(name.endsWith(" 1-15.png")).toBe(true);
    // 255 less the 9 bytes of " 1-15.png" leaves 246: 123 Hebrew letters of two bytes each, no room for more.
    expect(name).toBe(`${"א".repeat(123)} 1-15.png`);
    expect(bytes(name)).toBe(255);
  });

  it("cuts a long emoji name on a code point boundary, never through a surrogate pair", () => {
    const name = frameName(`${"🎬".repeat(100)}.webm`, 3725);
    expect(bytes(name)).toBeLessThanOrEqual(255);
    expect(name.endsWith(" 1-02-05.png")).toBe(true);
    const base = name.slice(0, name.length - " 1-02-05.png".length);
    // 255 less the 12 bytes of " 1-02-05.png" leaves 243: 60 emoji of four bytes each; the 61st would not fit.
    expect([...base]).toHaveLength(60);
    expect(base).toBe("🎬".repeat(60));
    // No lone surrogate remains; every pair is whole.
    expect(/[\uD800-\uDFFF]/.test(base.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, ""))).toBe(false);
  });

  it("cuts between scripts on the boundary too, keeping the time and .png whole", () => {
    const name = frameName(`${"א".repeat(122)}${"🎬".repeat(10)}.webm`, 1.2);
    expect(name).toBe(`${"א".repeat(122)} 0-01.png`);
    expect(bytes(name)).toBeLessThanOrEqual(255);
  });
});

// The canvas the capture draws on is stubbed: jsdom draws nothing. The stub keeps the element it was asked on, the
// type it was asked for and the video it was given, so the tests judge the size, the kind and the draw itself.
interface CanvasSeen {
  canvas: HTMLCanvasElement | null;
  type: string | undefined;
  drew: HTMLVideoElement | null;
}

function stubCanvas(opts: { draw?: () => void; blob?: Blob | null; toBlobThrows?: boolean } = {}): CanvasSeen {
  const seen: CanvasSeen = { canvas: null, type: undefined, drew: null };
  const drawImage = vi.fn((v: HTMLVideoElement) => {
    seen.drew = v;
    opts.draw?.();
  });
  Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value(this: HTMLCanvasElement, kind: string) {
      seen.canvas = this;
      return kind === "2d" ? ({ drawImage } as unknown as CanvasRenderingContext2D) : null;
    },
  });
  Object.defineProperty(HTMLCanvasElement.prototype, "toBlob", {
    configurable: true,
    value(this: HTMLCanvasElement, cb: BlobCallback, type?: string) {
      seen.type = type;
      if (opts.toBlobThrows) throw new DOMException("The canvas is tainted.", "SecurityError");
      cb(opts.blob === undefined ? new Blob(["png"], { type: "image/png" }) : opts.blob);
    },
  });
  return seen;
}

function video(over: { w?: number; h?: number; ready?: number } = {}): HTMLVideoElement {
  const v = document.createElement("video");
  Object.defineProperty(v, "videoWidth", { value: over.w ?? 160, configurable: true });
  Object.defineProperty(v, "videoHeight", { value: over.h ?? 120, configurable: true });
  Object.defineProperty(v, "readyState", { value: over.ready ?? 4, configurable: true });
  return v;
}

const proto = HTMLCanvasElement.prototype;
let kept: { getContext?: PropertyDescriptor; toBlob?: PropertyDescriptor } = {};

beforeEach(() => {
  kept = {
    getContext: Object.getOwnPropertyDescriptor(proto, "getContext"),
    toBlob: Object.getOwnPropertyDescriptor(proto, "toBlob"),
  };
});

afterEach(() => {
  for (const k of ["getContext", "toBlob"] as const) {
    const d = kept[k];
    if (d) Object.defineProperty(proto, k, d);
    else delete (proto as unknown as Record<string, unknown>)[k];
  }
  vi.restoreAllMocks();
});

describe("captureFrame", () => {
  it("draws the video on a canvas of its own size and keeps it as a PNG, with no canvas left in the document", async () => {
    const seen = stubCanvas();
    const v = video();
    const blob = await captureFrame(v);
    expect(seen.canvas?.width).toBe(160);
    expect(seen.canvas?.height).toBe(120);
    expect(seen.drew).toBe(v);
    expect(seen.type).toBe("image/png");
    expect(blob.type).toBe("image/png");
    expect(document.querySelector("canvas")).toBeNull();
    // Data at readyState 2 is enough; the size is the video's own, not the element's drawn box.
    const small = stubCanvas();
    await expect(captureFrame(video({ ready: 2, w: 320, h: 240 }))).resolves.toBeInstanceOf(Blob);
    expect(small.canvas?.width).toBe(320);
    expect(small.canvas?.height).toBe(240);
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("is not_ready while the video has no size", async () => {
    stubCanvas();
    await expect(captureFrame(video({ w: 0 }))).rejects.toMatchObject({ name: "FrameError", reason: "not_ready" });
    await expect(captureFrame(video({ h: 0 }))).rejects.toMatchObject({ name: "FrameError", reason: "not_ready" });
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("is not_ready before the video has data (readyState under 2)", async () => {
    stubCanvas();
    await expect(captureFrame(video({ ready: 1 }))).rejects.toMatchObject({ name: "FrameError", reason: "not_ready" });
    await expect(captureFrame(video({ ready: 0 }))).rejects.toMatchObject({ name: "FrameError", reason: "not_ready" });
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("is unreadable when the draw throws a SecurityError (a tainted canvas)", async () => {
    stubCanvas({ draw: () => { throw new DOMException("The canvas is tainted.", "SecurityError"); } });
    await expect(captureFrame(video())).rejects.toMatchObject({ name: "FrameError", reason: "unreadable" });
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("is unreadable when toBlob throws, and when it gives no blob", async () => {
    stubCanvas({ toBlobThrows: true });
    await expect(captureFrame(video())).rejects.toMatchObject({ name: "FrameError", reason: "unreadable" });
    stubCanvas({ blob: null });
    await expect(captureFrame(video())).rejects.toMatchObject({ name: "FrameError", reason: "unreadable" });
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("throws a FrameError, not a plain error", async () => {
    stubCanvas();
    await expect(captureFrame(video({ ready: 1 }))).rejects.toBeInstanceOf(FrameError);
    stubCanvas({ blob: null });
    await expect(captureFrame(video())).rejects.toBeInstanceOf(FrameError);
  });
});
