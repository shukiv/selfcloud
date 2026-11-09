// SPDX-License-Identifier: AGPL-3.0-or-later

import "../../test/rangeRects";
import { act, configure, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { PlaybackSeed } from "../../lib/files/playback";
import type { Node } from "../../lib/files/types";

// The playlist loads lazily, so the first wait for a video allows for that load.
configure({ asyncUtilTimeout: 5000 });


const signedLink = vi.fn();
const startDownload = vi.fn();
const startPlayback = vi.fn();
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  signedLink: (...args: unknown[]) => signedLink(...args),
}));
vi.mock("../../lib/files/ops", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/ops")>()),
  startDownload: (...args: unknown[]) => startDownload(...args),
}));
vi.mock("../../lib/files/playback", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/playback")>()),
  startPlayback: (...args: unknown[]) => startPlayback(...args),
}));

import Preview from "./Preview";

// codeShown waits for the code viewer and gives its lines' text, joined by line breaks.
async function codeShown(): Promise<string> {
  const content = await waitFor(() => {
    const el = document.querySelector(".cm-content");
    if (!el) throw new Error("no code viewer yet");
    return el;
  });
  return [...content.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n");
}

// rangeOf is the Range header of a fetch call.
const rangeOf = (call: unknown[]) => ((call[1] as RequestInit).headers as Record<string, string>).Range;

function file(name: string, mime: string, size = 10): Node {
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

describe("Preview", () => {
  beforeEach(() => {
    signedLink.mockReset();
    startDownload.mockReset();
    startPlayback.mockReset();
    signedLink.mockResolvedValue({ url: "https://content.example/x?sig=1", disposition: "inline", expires_at: "" });
    startDownload.mockResolvedValue(true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows an image through its signed link", async () => {
    render(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    const img = (await screen.findByRole("img", { name: "cat.png" })) as HTMLImageElement;
    expect(img.src).toBe("https://content.example/x?sig=1");
    expect(signedLink).toHaveBeenCalledWith("n-cat.png", "inline", 300);
  });

  // A link the page loads must be http, https or the same origin. A script or data address the server might send
  // is never put in an element's source.
  it.each([
    ["javascript:", "javascript:alert(1)"],
    ["data:", "data:text/html,<script>alert(1)</script>"],
  ])("refuses a %s link in an image, a frame, a video and an audio element", async (_name, url) => {
    signedLink.mockResolvedValue({ url, disposition: "inline", expires_at: "" });
    const { unmount } = render(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
    unmount();
    for (const [name, mime] of [["plan.pdf", "application/pdf"], ["clip.mp4", "video/mp4"], ["song.mp3", "audio/mpeg"]]) {
      signedLink.mockResolvedValue({ url, disposition: "inline", expires_at: "" });
      const r = render(<Preview items={[file(name, mime)]} index={0} onClose={() => {}} onIndex={() => {}} />);
      expect(await screen.findByRole("alert")).toBeTruthy();
      expect(r.container.querySelector("iframe, video, audio, img")).toBeNull();
      r.unmount();
    }
  });

  it("opens a PDF in a frame titled with the name", async () => {
    render(<Preview items={[file("plan.pdf", "application/pdf")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    const frame = await waitFor(() => {
      const el = document.querySelector("iframe");
      if (!el) throw new Error("no frame yet");
      return el;
    });
    expect(frame.title).toBe("plan.pdf");
    expect(frame.getAttribute("src")).toBe("https://content.example/x?sig=1");
  });

  it("sniffs the first 8 KiB, reads the first 1 MiB on this origin, shows it in the code viewer and says when it is cut short", async () => {
    const fetchMock = vi.fn(async () => new Response("<b>not html</b>", { status: 206 }));
    vi.stubGlobal("fetch", fetchMock);
    render(
      <Preview items={[file("notes.txt", "text/plain", 2 * 1024 * 1024)]} index={0} onClose={() => {}} onIndex={() => {}} />,
    );
    expect(await codeShown()).toBe("<b>not html</b>");
    // CodeMirror replaces the plain text block, and the text is never read as markup.
    expect(document.querySelector("pre")).toBeNull();
    expect(document.querySelector(".cm-content b")).toBeNull();
    expect(screen.getByText("Showing the first 1 MB.")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(rangeOf(calls[0])).toBe("bytes=0-8191");
    expect(rangeOf(calls[1])).toBe("bytes=0-1048575");
    for (const [url, init] of calls) {
      expect(url).toBe("/api/v1/nodes/n-notes.txt/content?disposition=inline");
      expect(init.credentials).toBe("same-origin");
      expect(init.cache).toBe("no-store");
    }
  });

  it("reads a file no larger than the sniff once, and shows no notice", async () => {
    const fetchMock = vi.fn(async () => new Response("package main\n", { status: 206 }));
    vi.stubGlobal("fetch", fetchMock);
    render(<Preview items={[file("main.go", "application/octet-stream", 13)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await codeShown()).toBe("package main\n");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Showing the first 1 MB.")).toBeNull();
  });

  it("shows an empty file as antd's Empty, with no read", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    render(<Preview items={[file("empty.txt", "text/plain", 0)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByText("This file is empty.")).toBeTruthy();
    expect(document.querySelector(".ant-empty")).not.toBeNull();
    expect(document.querySelector(".cm-editor")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows the card, not the code viewer, for a text file the sniff finds binary", async () => {
    const fetchMock = vi.fn(async () => new Response(new Uint8Array([0x61, 0x00, 0x62]), { status: 206 }));
    vi.stubGlobal("fetch", fetchMock);
    render(<Preview items={[file("notes.txt", "text/plain", 3)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByText("No preview for this file. Download it to open it.")).toBeTruthy();
    expect(document.querySelector(".cm-editor")).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("opens a file stored as octet-stream as plain text when the sniff finds text", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("MIT License\n", { status: 206 })));
    render(<Preview items={[file("LICENSE", "application/octet-stream", 12)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await codeShown()).toBe("MIT License\n");
  });

  it("offers Find in the header while the code viewer shows text, and it opens the find panel", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("alpha\nbeta", { status: 206 })));
    render(<Preview items={[file("notes.txt", "text/plain", 10)]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await codeShown();
    const findButton = await screen.findByRole("button", { name: "Find" });
    fireEvent.click(findButton);
    expect(await screen.findByRole("textbox", { name: "Find" })).toBeTruthy();
  });

  it("keeps the arrows pressed in the code viewer there: they do not step to the next file", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("alpha\nbeta", { status: 206 })));
    const onIndex = vi.fn();
    const items = [file("a.txt", "text/plain", 10), file("b.txt", "text/plain", 10)];
    render(<Preview items={items} index={0} onClose={() => {}} onIndex={onIndex} />);
    await codeShown();
    fireEvent.keyDown(document.querySelector(".cm-content")!, { key: "ArrowRight" });
    expect(onIndex).not.toHaveBeenCalled();
    fireEvent.keyDown(document.querySelector("[data-preview-stage]")!, { key: "ArrowRight" });
    expect(onIndex).toHaveBeenCalledWith(1);
  });

  it("shows a .ts file stored as video/mp2t as code when the sniff finds text, and does not play it", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("export const a = 1;\n", { status: 206 })));
    const ts = file("app.ts", "video/mp2t", 20);
    const seed = { start: ts, folder: {} } as unknown as PlaybackSeed;
    render(<Preview items={[ts]} index={0} onClose={() => {}} onIndex={() => {}} media={seed} />);
    expect(await codeShown()).toBe("export const a = 1;\n");
    expect(startPlayback).not.toHaveBeenCalled();
  });

  it("plays a .ts file stored as video/mp2t when the sniff finds it binary", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array([0x47, 0x00, 0x11]), { status: 206 })));
    const ts = file("clip.ts", "video/mp2t", 4 * 1024 * 1024);
    const seed = { start: ts, folder: {} } as unknown as PlaybackSeed;
    render(<Preview items={[ts]} index={0} onClose={() => {}} onIndex={() => {}} media={seed} />);
    await waitFor(() => expect(startPlayback).toHaveBeenCalledTimes(1));
    expect(document.querySelector(".cm-editor")).toBeNull();
  });

  it("plays a video whose name is not in the code list without a sniff", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const clip = file("clip.mp4", "video/mp4", 4 * 1024 * 1024);
    const seed = { start: clip, folder: {} } as unknown as PlaybackSeed;
    render(<Preview items={[clip]} index={0} onClose={() => {}} onIndex={() => {}} media={seed} />);
    expect(startPlayback).toHaveBeenCalledTimes(1);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("says downloads are off when the link is refused, and not as an error", async () => {
    signedLink.mockRejectedValue(new ApiError(403, "download_restricted", {}, 0));
    render(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByText("Downloads are off for this file.")).toBeTruthy();
  });

  it("says the text is restricted when the read is refused with 403", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 403 })));
    render(<Preview items={[file("notes.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByText("Downloads are off for this file.")).toBeTruthy();
  });

  it("shows the generic error for any other failure", async () => {
    signedLink.mockRejectedValue(new ApiError(500, "internal", {}, 0));
    render(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await screen.findByText("Something went wrong. Try again.")).toBeTruthy();
  });

  it("gives a card with no inline preview for a file it cannot show", () => {
    render(<Preview items={[file("sheet.xlsx", "application/vnd.ms-excel")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(screen.getByText("No preview for this file. Download it to open it.")).toBeTruthy();
    expect(signedLink).not.toHaveBeenCalled();
  });

  it("steps with the arrows, mirrored in right-to-left", () => {
    const onIndex = vi.fn();
    const items = [file("a.png", "image/png"), file("b.png", "image/png")];
    const { unmount } = render(<Preview items={items} index={0} onClose={() => {}} onIndex={onIndex} />);
    fireEvent.keyDown(document.querySelector('[aria-label="Previous and next file"]')!.parentElement!, { key: "ArrowRight" });
    expect(onIndex).toHaveBeenLastCalledWith(1);
    unmount();
    document.documentElement.dir = "rtl";
    const rtl = render(<Preview items={items} index={0} onClose={() => {}} onIndex={onIndex} />);
    fireEvent.keyDown(rtl.container.ownerDocument.querySelector('[aria-label="Previous and next file"]')!.parentElement!, { key: "ArrowRight" });
    expect(onIndex).toHaveBeenCalledTimes(1);
    document.documentElement.dir = "ltr";
  });

  it("downloads through startDownload and reports a refusal", async () => {
    startDownload.mockResolvedValue(false);
    render(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await screen.findByRole("img", { name: "cat.png" });
    fireEvent.click(screen.getAllByRole("button", { name: "Download" })[0]);
    expect(startDownload).toHaveBeenCalledWith("n-cat.png");
    expect(await screen.findByText("Downloads are off for this file.")).toBeTruthy();
  });
});

describe("Preview direction icons", () => {
  it("draws the previous and next chevrons as icons that mirror in right to left", () => {
    const items = [file("a.png", "image/png"), file("b.png", "image/png")];
    const { container } = render(<Preview items={items} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(container.ownerDocument.querySelectorAll(".icon-dir").length).toBeGreaterThanOrEqual(1);
    const next = document.querySelector("button .icon-dir");
    expect(next).not.toBeNull();
  });
});

describe("Preview drops a read that finishes after the person moved on", () => {
  it("does not show the text of a file the person has left", async () => {
    let release!: (r: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => (release = resolve))));
    const { rerender } = render(<Preview items={[file("notes.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    rerender(<Preview items={[file("cat.png", "image/png")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await act(async () => release(new Response("old text", { status: 200 })));
    await new Promise((r) => setTimeout(r, 20));
    expect(document.querySelector(".cm-editor")).toBeNull();
  });
});
describe("Preview keeps the text of the file on screen", () => {
  it("does not replace the file on screen with an earlier file's text that arrives late", async () => {
    // The first answer arrives at once, but its body is read slowly: the person moves on while the text is still read.
    let releaseA!: (body: ArrayBuffer) => void;
    const slowBody = { ok: true, status: 200, arrayBuffer: () => new Promise<ArrayBuffer>((resolve) => (releaseA = resolve)) } as unknown as Response;
    vi.stubGlobal(
      "fetch",
      vi.fn()
        .mockImplementationOnce(async () => slowBody)
        .mockImplementationOnce(async () => new Response("bee text", { status: 200 })),
    );
    const { rerender } = render(<Preview items={[file("a.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await waitFor(() => expect(releaseA).toBeDefined());
    rerender(<Preview items={[file("b.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    expect(await codeShown()).toBe("bee text");
    await act(async () => releaseA(new TextEncoder().encode("ay text").buffer as ArrayBuffer));
    await new Promise((r) => setTimeout(r, 20));
    expect(await codeShown()).toBe("bee text");
  });

});

// The link on screen belongs to the file on screen. A state recorded for another file is not shown.
describe("Preview shows only the state of the file on screen", () => {
  it("shows a loading card for a file whose state was recorded for the file before it", async () => {
    const { shownState } = await import("./Preview");
    const ready = { id: "n-a.mp4", state: { status: "ready" as const, link: { url: "https://content.example/a" } } };
    expect(shownState(ready, "n-a.mp4")).toBe(ready.state);
    expect(shownState(ready, "n-b.mp4")).toEqual({ status: "loading" });
    expect(shownState({ id: null, state: { status: "error" } }, "n-a.mp4")).toEqual({ status: "loading" });
  });
});
