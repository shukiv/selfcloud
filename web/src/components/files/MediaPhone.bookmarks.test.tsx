// SPDX-License-Identifier: AGPL-3.0-or-later

// The album page's More menu gains the bookmark items only when the player has marks: Add a bookmark (disabled until the
// list is read, when full, or with no element) and Bookmarks (n), which opens the list.

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { loadMedia } from "../../lib/media/i18n";
import { resetTags } from "../../lib/files/tags";
import { folders, hiddenFolders } from "../../lib/files/folders";
import type { Bookmark } from "../../lib/files/api";
import type { Marks } from "../../lib/files/bookmarks";
import type { Node } from "../../lib/files/types";
import MediaPhone from "./MediaPhone";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({ id: "bmk_1", position_seconds: 10, label: "", created_at: "2026-11-16T00:00:00Z", ...over });

function marks(items: Bookmark[], over: Partial<Marks> = {}): Marks {
  return { items, ready: true, full: false, add: vi.fn(), openList: vi.fn(), ...over };
}

function file(name: string): Node {
  return {
    id: `nod_${name}`, folder_id: "f1", parent_id: "d1", name, kind: "file", size: 1, mime_type: "audio/mpeg",
    created_at: "", modified_at: "", role: "editor", no_download: false, editors_can_share: false,
  } as unknown as Node;
}

function props(mk?: Marks, media: HTMLMediaElement | null = document.createElement("audio")) {
  return {
    node: file("song.mp3"),
    position: 1,
    media,
    playing: false,
    nav: { next: vi.fn(), previous: vi.fn() },
    onPress: vi.fn(),
    onPlaylist: vi.fn(),
    onDownload: vi.fn(),
    onDetails: vi.fn(),
    shuffle: false,
    repeat: "off" as const,
    onShuffle: vi.fn(),
    onRepeat: vi.fn(),
    marks: mk,
  };
}

const draw = (mk?: Marks, media: HTMLMediaElement | null = document.createElement("audio")) =>
  render(
    <MemoryRouter initialEntries={["/media"]}>
      <MediaPhone {...props(mk, media)} />
    </MemoryRouter>,
  );

async function openMore() {
  fireEvent.click(screen.getByTestId("media-phone-more"));
  return screen.findByRole("menuitem", { name: /^Speed/ });
}

beforeAll(async () => {
  await loadMedia("en");
});

beforeEach(() => {
  resetTags();
  folders.set([]);
  hiddenFolders.set([]);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  resetTags();
});

describe("the album page's More menu bookmark items", () => {
  it("shows Add a bookmark and Bookmarks (n) only with marks, and they run the marks' actions", async () => {
    const mk = marks([b(), b({ id: "bmk_2" }), b({ id: "bmk_3" })]);
    draw(mk);
    await openMore();
    const add = await screen.findByRole("menuitem", { name: "Add a bookmark" });
    const list = await screen.findByRole("menuitem", { name: "Bookmarks (3)" });
    fireEvent.click(add);
    expect(mk.add).toHaveBeenCalledTimes(1);
    fireEvent.click(list);
    expect(mk.openList).toHaveBeenCalledTimes(1);
  });

  it("draws neither item without marks", async () => {
    draw();
    await openMore();
    expect(screen.queryByRole("menuitem", { name: "Add a bookmark" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /^Bookmarks/ })).toBeNull();
  });

  it("disables Add until the list is read, when full, or with no element", async () => {
    const notReady = draw(marks([], { ready: false }));
    await openMore();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
    notReady.unmount();

    const full = draw(marks([b()], { full: true }));
    await openMore();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
    full.unmount();

    draw(marks([b()]), null);
    await openMore();
    expect((await screen.findByRole("menuitem", { name: "Add a bookmark" })).getAttribute("aria-disabled")).toBe("true");
  });
});
