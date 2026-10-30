// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";

const signedLink = vi.fn();
const startDownload = vi.fn();
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  signedLink: (...args: unknown[]) => signedLink(...args),
}));
vi.mock("../../lib/files/ops", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/ops")>()),
  startDownload: (...args: unknown[]) => startDownload(...args),
}));

import Preview from "./Preview";

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

  it("reads text on this origin as text, and says when it is cut short", async () => {
    const fetchMock = vi.fn(async () => new Response("<b>not html</b>", { status: 206 }));
    vi.stubGlobal("fetch", fetchMock);
    render(
      <Preview items={[file("notes.txt", "text/plain", 2 * 1024 * 1024)]} index={0} onClose={() => {}} onIndex={() => {}} />,
    );
    const pre = await waitFor(() => {
      const el = document.querySelector("pre");
      if (!el) throw new Error("no text yet");
      return el;
    });
    expect(pre.textContent).toBe("<b>not html</b>");
    expect(document.querySelector("pre b")).toBeNull();
    expect(screen.getByText("Showing the first 1 MB.")).toBeTruthy();
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.credentials).toBe("same-origin");
    expect(init.cache).toBe("no-store");
    expect((init.headers as Record<string, string>).Range).toBe("bytes=0-1048575");
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
    expect(document.querySelector("pre")).toBeNull();
  });
});
describe("Preview keeps the text of the file on screen", () => {
  it("does not replace the file on screen with an earlier file's text that arrives late", async () => {
    // The first answer arrives at once, but its body is read slowly: the person moves on while the text is still read.
    let releaseA!: (text: string) => void;
    const slowBody = { ok: true, status: 200, text: () => new Promise<string>((resolve) => (releaseA = resolve)) } as unknown as Response;
    vi.stubGlobal(
      "fetch",
      vi.fn()
        .mockImplementationOnce(async () => slowBody)
        .mockImplementationOnce(async () => new Response("bee text", { status: 200 })),
    );
    const { rerender } = render(<Preview items={[file("a.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await waitFor(() => expect(releaseA).toBeDefined());
    rerender(<Preview items={[file("b.txt", "text/plain")]} index={0} onClose={() => {}} onIndex={() => {}} />);
    await waitFor(() => expect(document.querySelector("pre")?.textContent).toBe("bee text"));
    await act(async () => releaseA("ay text"));
    await new Promise((r) => setTimeout(r, 20));
    expect(document.querySelector("pre")?.textContent).toBe("bee text");
  });
});
