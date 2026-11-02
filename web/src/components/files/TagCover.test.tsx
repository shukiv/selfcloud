// SPDX-License-Identifier: AGPL-3.0-or-later

import { useLayoutEffect } from "react";
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import TagCover from "./TagCover";
import { watchThumbnail } from "../../lib/files/thumbnail";

// The cover is asked for through watchThumbnail. The spy records whether a watch was started; the real one runs.
vi.mock("../../lib/files/thumbnail", async (orig) => {
  const real = await orig<typeof import("../../lib/files/thumbnail")>();
  return { ...real, watchThumbnail: vi.fn(real.watchThumbnail) };
});

// The cover box has its size from the first render, a plain icon under the picture, and asks for the cover only when enabled.
describe("TagCover: a fixed box, an icon until the cover is ready", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    vi.mocked(watchThumbnail).mockClear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows the icon at the given size, and starts no watch while not enabled", async () => {
    await act(async () => {
      render(<TagCover nod="nod_a" kind="audio" size={40} enabled={false} />);
    });
    const box = screen.getByTestId("tag-cover");
    expect(box.getAttribute("data-cover")).toBe("no");
    expect(box.style.width).toBe("40px");
    expect(box.style.height).toBe("40px");
    expect(box.querySelector("img")).toBeNull();
    expect(box.querySelector("svg")).not.toBeNull();
    expect(watchThumbnail).not.toHaveBeenCalled();
  });

  it("starts one watch for the cover when enabled, and keeps the icon when there is none", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 404 }));
    await act(async () => {
      render(<TagCover nod="nod_a" kind="audio" size={28} enabled />);
    });
    expect(watchThumbnail).toHaveBeenCalledTimes(1);
    expect(vi.mocked(watchThumbnail).mock.calls[0][0]).toBe("/api/v1/nodes/nod_a/thumbnail?size=256");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(screen.getByTestId("tag-cover").querySelector("img")).toBeNull();
  });

  it("shows the picture once the cover is ready", async () => {
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:cover"), revokeObjectURL: vi.fn() }));
    fetchMock.mockResolvedValue(new Response(new Uint8Array([0xff, 0xd8, 0xff]), { status: 200 }));
    await act(async () => {
      render(<TagCover nod="nod_a" kind="audio" size={64} enabled />);
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    const box = screen.getByTestId("tag-cover");
    expect(box.getAttribute("data-cover")).toBe("yes");
    expect(box.querySelector("img")?.getAttribute("src")).toBe("blob:cover");
    expect(box.style.width).toBe("64px");
  });

  // The first frame of a new node must not show the previous file's cover: a layout effect runs in the same commit as the
  // DOM, so what it reads is what the first frame shows (the effect that clears the cover runs after that).
  it("shows no previous cover on the first frame of a new node", async () => {
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:cover"), revokeObjectURL: vi.fn() }));
    fetchMock.mockResolvedValueOnce(new Response(new Uint8Array([0xff, 0xd8, 0xff]), { status: 200 }));
    const firstFrame: string[] = [];
    function Probe() {
      useLayoutEffect(() => {
        firstFrame.push(document.querySelector('[data-testid="tag-cover"]')?.getAttribute("data-cover") ?? "missing");
      });
      return null;
    }
    const view = render(
      <>
        <TagCover nod="nod_a" kind="audio" size={64} enabled />
        <Probe />
      </>,
    );
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(screen.getByTestId("tag-cover").getAttribute("data-cover")).toBe("yes");

    fetchMock.mockReturnValueOnce(new Promise(() => undefined));
    firstFrame.length = 0;
    view.rerender(
      <>
        <TagCover nod="nod_b" kind="audio" size={64} enabled />
        <Probe />
      </>,
    );
    expect(firstFrame[0]).toBe("no");
    expect(screen.getByTestId("tag-cover").querySelector("img")).toBeNull();
  });
});
