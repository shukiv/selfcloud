// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import { session, type Session } from "../../lib/session";

// signedIn is the session a person has; the share page has none (MediaArt asks for no picture then).
const signedIn = { user: { id: "u" } } as unknown as Session;

// The thumbnail watch is replaced by a recorder: each call is a request for a path, and the test answers it through the sink.
const asks: { path: string; sink: Record<string, (...a: unknown[]) => void> }[] = [];
vi.mock("../../lib/files/thumbnail", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/thumbnail")>()),
  watchThumbnail: (path: string, sink: Record<string, (...a: unknown[]) => void>) => {
    asks.push({ path, sink });
    return () => undefined;
  },
}));

const listing = { value: null as null | { items: Node[]; complete: boolean; folderName?: string } };
const folderGet = vi.fn(async (_n: Node) => listing.value);
vi.mock("../../lib/files/folderCover", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/folderCover")>()),
  folderCovers: { get: (n: Node) => folderGet(n), reset: () => undefined },
}));

const { default: MediaArt } = await import("./MediaArt");

function track(name: string, id = name): Node {
  return { id, name, kind: "file", folder_id: "f", parent_id: "d", size: 1, created_at: "", modified_at: "", no_download: false, editors_can_share: false } as Node;
}

// answer is the server's answer to the last request for a path: an image (a blob address) or none.
function answer(path: string, what: "image" | "none", src = `blob:${path}`) {
  const ask = [...asks].reverse().find((a) => a.path === path);
  if (!ask) throw new Error(`no request for ${path}`);
  act(() => {
    if (what === "image") ask.sink.image(src);
    else ask.sink.none();
  });
}

const cover = (container: HTMLElement) => container.querySelector('[data-testid="media-cover"]') as HTMLElement;

describe("MediaArt (decision 63)", () => {
  beforeEach(() => {
    asks.length = 0;
    listing.value = null;
    folderGet.mockClear();
    session.set(signedIn);
  });

  it("asks for no picture and no listing without a session (a share visitor): the icon holds the place", async () => {
    session.set(null);
    listing.value = { items: [track("cover.jpg"), track("01.mp3")], complete: true, folderName: "Album" };
    const { container } = render(<MediaArt node={track("01.mp3")} />);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(asks).toEqual([]);
    expect(folderGet).not.toHaveBeenCalled();
    expect(cover(container).dataset.cover).toBe("no");
    expect(container.querySelector(".media-cover-empty svg")).not.toBeNull();
  });

  it("keeps the folder's picture across a step inside the album: same element, no second request for it", async () => {
    listing.value = { items: [track("cover.jpg"), track("01.mp3"), track("02.mp3")], complete: true, folderName: "Album" };
    const { container, rerender } = render(<MediaArt node={track("01.mp3")} />);
    answer("/api/v1/nodes/01.mp3/thumbnail?size=1024", "none");
    answer("/api/v1/nodes/01.mp3/thumbnail?size=256", "none");
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    answer("/api/v1/nodes/cover.jpg/thumbnail?size=1024", "image");
    const img = container.querySelector("img");
    expect(img).not.toBeNull();
    const asksForCover = () => asks.filter((a) => a.path === "/api/v1/nodes/cover.jpg/thumbnail?size=1024").length;
    expect(asksForCover()).toBe(1);

    rerender(<MediaArt node={track("02.mp3")} />);
    // Track 02 has its own probes; its picture is the folder's, which stays on the stage the whole time.
    expect(container.querySelector("img")).toBe(img);
    expect(cover(container).dataset.cover).toBe("yes");
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    answer("/api/v1/nodes/02.mp3/thumbnail?size=1024", "none");
    answer("/api/v1/nodes/02.mp3/thumbnail?size=256", "none");
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(container.querySelector("img")).toBe(img);
    expect(asksForCover()).toBe(1);
    expect(cover(container).dataset.source).toBe("folder");
  });

  it("shows the file's own picture at the largest size, and reads no folder for it", () => {
    const a = track("a.mp3");
    const { container } = render(<MediaArt node={a} />);
    expect(asks.map((x) => x.path)).toEqual(["/api/v1/nodes/a.mp3/thumbnail?size=1024"]);
    answer("/api/v1/nodes/a.mp3/thumbnail?size=1024", "image");
    expect(cover(container).dataset.cover).toBe("yes");
    expect(cover(container).dataset.source).toBe("file");
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:/api/v1/nodes/a.mp3/thumbnail?size=1024");
    expect(folderGet).not.toHaveBeenCalled();
  });

  it("asks the smaller size when the largest has no picture, before it looks at the folder", () => {
    const a = track("a.mp3");
    const { container } = render(<MediaArt node={a} />);
    answer("/api/v1/nodes/a.mp3/thumbnail?size=1024", "none");
    expect(asks.at(-1)?.path).toBe("/api/v1/nodes/a.mp3/thumbnail?size=256");
    answer("/api/v1/nodes/a.mp3/thumbnail?size=256", "image");
    expect(cover(container).dataset.source).toBe("file");
    expect(folderGet).not.toHaveBeenCalled();
  });

  it("takes the folder's picture for a file with none of its own, read once for the folder", async () => {
    listing.value = { items: [track("cover.jpg"), track("01.mp3"), track("02.mp3")], complete: true, folderName: "Album" };
    const a = track("01.mp3");
    const { container } = render(<MediaArt node={a} />);
    answer("/api/v1/nodes/01.mp3/thumbnail?size=1024", "none");
    answer("/api/v1/nodes/01.mp3/thumbnail?size=256", "none");
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(folderGet).toHaveBeenCalledTimes(1);
    expect(asks.at(-1)?.path).toBe("/api/v1/nodes/cover.jpg/thumbnail?size=1024");
    answer("/api/v1/nodes/cover.jpg/thumbnail?size=1024", "image");
    expect(cover(container).dataset.source).toBe("folder");
  });

  it("shows a tinted box with a music icon when neither the file nor the folder has a picture", async () => {
    listing.value = { items: [track("01.mp3")], complete: true, folderName: "Album" };
    const { container } = render(<MediaArt node={track("01.mp3")} />);
    answer("/api/v1/nodes/01.mp3/thumbnail?size=1024", "none");
    answer("/api/v1/nodes/01.mp3/thumbnail?size=256", "none");
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(cover(container).dataset.cover).toBe("no");
    expect(container.querySelector(".media-cover-empty svg")).not.toBeNull();
  });

  it("draws no picture of the previous track for a step to the next one", () => {
    const { container, rerender } = render(<MediaArt node={track("a.mp3")} />);
    answer("/api/v1/nodes/a.mp3/thumbnail?size=1024", "image");
    expect(container.querySelector("img")).not.toBeNull();
    rerender(<MediaArt node={track("b.mp3")} />);
    // Before the server answers for b, no picture is drawn: a's picture is not kept on the stage.
    expect(container.querySelector("img")).toBeNull();
    expect(cover(container).dataset.cover).toBe("no");
  });

  it("shows the name and the artist under the cover, as the file's name when there are no tags", () => {
    const { container } = render(<MediaArt node={track("plain-file.mp3")} />);
    expect(container.querySelector(".media-art-name bdi")?.textContent).toBe("plain-file.mp3");
    expect(container.querySelector('[data-testid="media-artist"]')?.textContent).toBe("");
  });
});
