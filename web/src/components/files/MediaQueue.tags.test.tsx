// SPDX-License-Identifier: AGPL-3.0-or-later

// A playlist row shows a file's title, artist and cover when they are known, and its file name and a plain icon when not.

import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { markCover, resetTags, seedTags } from "../../lib/files/tags";
import { watchThumbnail } from "../../lib/files/thumbnail";
import type { Entry } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import { session } from "../../lib/session";
import MediaQueue from "./MediaQueue";

// The cover is asked for through watchThumbnail. The spy records which watches were started; the real one runs.
vi.mock("../../lib/files/thumbnail", async (orig) => {
  const real = await orig<typeof import("../../lib/files/thumbnail")>();
  return { ...real, watchThumbnail: vi.fn(real.watchThumbnail) };
});

function node(name: string, mime: string, extra: Partial<Node> = {}): Node {
  return { id: `nod_${name}`, folder_id: "f", parent_id: "d", name, kind: "file", mime_type: mime, size: 1, created_at: "", updated_at: "", ...extra } as unknown as Node;
}

function queue(nodes: Node[]) {
  const entries: Entry[] = nodes.map((n) => ({ node: n, mark: "ok" }));
  return render(
    <MediaQueue
      entries={entries}
      currentId={null}
      repeat="off"
      shuffle={false}
      capped={false}
      moreLeft={false}
      busy={false}
      candidates={[]}
      adding={false}
      onAddOpen={() => {}}
      onAdd={() => {}}
      onLoadMore={() => {}}
      onPlay={vi.fn()}
      onRemove={() => {}}
      onMove={() => {}}
      onShuffle={() => {}}
      onRepeat={() => {}}
    />,
  );
}

const rows = () => screen.getAllByTestId("media-row");

beforeEach(() => {
  resetTags();
  vi.mocked(watchThumbnail).mockClear();
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("MediaQueue rows and tags", () => {
  it("shows the file name and a plain icon when nothing is known, with the box in place", () => {
    queue([node("a.ogg", "audio/ogg")]);
    const row = rows()[0];
    expect(row.querySelector(".media-title bdi")?.textContent).toBe("a.ogg");
    expect(row.querySelector(".media-row-artist")).toBeNull();
    const box = row.querySelector('[data-testid="tag-cover"]') as HTMLElement;
    expect(box.style.width).toBe("28px");
    expect(box.querySelector("svg")).not.toBeNull();
  });

  it("gives a video row the film icon, and asks for no cover", () => {
    queue([node("v.webm", "video/webm", { has_thumbnail: true })]);
    const box = rows()[0].querySelector('[data-testid="tag-cover"]') as HTMLElement;
    expect(box.dataset.kind).toBe("video");
    expect(watchThumbnail).not.toHaveBeenCalled();
  });

  it("shows the listing's tags in place of the name, as text, with dir=auto, and keeps the file name in the tooltip", async () => {
    queue([node("a.mp3", "audio/mpeg", { tags: { title: "<b>Song</b>", artist: "שלום" } })]);
    await waitFor(() => expect(rows()[0].querySelector(".media-title bdi")?.textContent).toBe("<b>Song</b>"));
    const row = rows()[0];
    expect(row.querySelector(".media-title b")).toBeNull();
    expect(row.querySelector(".media-title bdi")?.getAttribute("dir")).toBe("auto");
    const artist = row.querySelector(".media-row-artist");
    expect(artist?.textContent).toBe("שלום");
    expect(artist?.getAttribute("dir")).toBe("auto");
    expect(row.querySelector(".media-title")?.getAttribute("title")).toBe("a.mp3");
  });

  it("asks for the audio files' tags only, not a video's", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ items: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    act(() => session.set({ user: { id: "u", email: "u@x.test" }, account: null, csrf: "c", publicLinks: false } as never));
    queue([node("a.mp3", "audio/mpeg"), node("v.webm", "video/webm")]);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const url = String((fetchMock.mock.calls as unknown as unknown[][])[0][0]);
    expect(url).toContain("/api/v1/media/tags?ids=nod_a.mp3");
    expect(url).not.toContain("v.webm");
    act(() => session.set(null));
  });

  it("asks for a row's cover only once the file is known to have one", () => {
    queue([node("a.mp3", "audio/mpeg", { has_thumbnail: false })]);
    expect(watchThumbnail).not.toHaveBeenCalled();
    act(() => markCover("nod_a.mp3"));
    expect(watchThumbnail).toHaveBeenCalledTimes(1);
  });

  it("asks for a row's cover when the listing says there is one", () => {
    queue([node("b.mp3", "audio/mpeg", { has_thumbnail: true })]);
    expect(watchThumbnail).toHaveBeenCalledTimes(1);
  });

  it("follows a tag that arrives after the first render", () => {
    queue([node("a.mp3", "audio/mpeg")]);
    expect(rows()[0].querySelector(".media-title bdi")?.textContent).toBe("a.mp3");
    act(() => seedTags("nod_a.mp3", { title: "Late" }));
    expect(rows()[0].querySelector(".media-title bdi")?.textContent).toBe("Late");
  });
});
