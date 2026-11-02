// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { createFolderCovers, pickFolderCover, readFolder, PAGES_MAX, LISTING_TTL_MS, type Listing } from "./folderCover";
import type { Node } from "./types";

// The browser's listing and stat, replaced by a recorder: pages of the folder by cursor, and the parent's name.
const pages = vi.hoisted(() => ({ list: [] as { items: unknown[]; next_cursor?: string }[], calls: [] as (string | undefined)[], fail: false, name: "Album" }));
vi.mock("./api", () => ({
  childrenPage: vi.fn(async (_f: string, _d: string | null, cursor?: string) => {
    pages.calls.push(cursor);
    if (pages.fail) throw new Error("down");
    const i = cursor === undefined ? 0 : Number(cursor);
    return pages.list[i] ?? { items: [], next_cursor: undefined };
  }),
  statNode: vi.fn(async () => ({ name: pages.name })),
}));

function file(name: string, id = name): Node {
  return { id, name, kind: "file", folder_id: "f", parent_id: "d", size: 1, created_at: "", modified_at: "", no_download: false, editors_can_share: false } as Node;
}

const cover = (name: string) => file(name);
const tracks = Array.from({ length: 14 }, (_, i) => file(`${String(i + 1).padStart(2, "0")} track.mp3`));

describe("pickFolderCover", () => {
  it("takes cover, folder, front or album, in that order, whatever the case of the name", () => {
    const items = [cover("Front.JPG"), cover("Folder.png"), ...tracks];
    expect(pickFolderCover(items, { complete: true })?.name).toBe("Folder.png");
    expect(pickFolderCover([cover("album.webp"), cover("FRONT.jpeg")], { complete: true })?.name).toBe("FRONT.jpeg");
    expect(pickFolderCover([cover("cover.jpg"), cover("front.jpg")], { complete: true })?.name).toBe("cover.jpg");
  });

  it("takes an image named after the album tag, the real shape: no art in the tracks and one image", () => {
    const items = [cover("Sample Album (Demo Edition).jpg"), ...tracks];
    const got = pickFolderCover(items, { album: "Sample Album (Demo Edition)", folderName: "2003 - Sample Album (Demo Edition)", complete: true });
    expect(got?.name).toBe("Sample Album (Demo Edition).jpg");
  });

  it("takes an image whose name is contained in the folder's name", () => {
    const items = [cover("Demo Edition.png"), cover("other.jpg"), ...tracks];
    expect(pickFolderCover(items, { folderName: "2003 - Demo Edition", complete: true })?.name).toBe("Demo Edition.png");
  });

  it("takes the one image of the folder, whatever its name, when the whole folder was read", () => {
    expect(pickFolderCover([cover("scan-0412.jpg"), ...tracks], { complete: true })?.name).toBe("scan-0412.jpg");
  });

  it("takes no image when the folder holds two or more and none of them matches", () => {
    expect(pickFolderCover([cover("scan-1.jpg"), cover("scan-2.jpg"), ...tracks], { complete: true })).toBeNull();
  });

  it("takes no image of the lone kind when only a page of the folder was read", () => {
    expect(pickFolderCover([cover("scan-0412.jpg"), ...tracks], { complete: false })).toBeNull();
  });

  it("takes no non-image: an audio file, a folder or a text file never counts", () => {
    expect(pickFolderCover([file("cover.txt"), file("front.mp3"), tracks[0]], { complete: true })).toBeNull();
    const dir = { ...cover("cover.jpg"), kind: "dir" } as Node;
    expect(pickFolderCover([dir], { complete: true })).toBeNull();
  });

  it("gives no image for an empty folder", () => {
    expect(pickFolderCover([], { complete: true })).toBeNull();
  });
});

describe("createFolderCovers", () => {
  const listing: Listing = { items: [cover("cover.jpg"), ...tracks], complete: true, folderName: "Album" };

  it("reads each folder once, however many of its tracks ask", async () => {
    const read = vi.fn(async () => listing);
    const covers = createFolderCovers(read);
    const a = file("a.mp3", "a");
    const b = file("b.mp3", "b");
    const [x, y] = await Promise.all([covers.get(a), covers.get(b)]);
    await covers.get(a);
    expect(read).toHaveBeenCalledTimes(1);
    expect(x).toBe(y);
  });

  it("reads a folder again only for another folder", async () => {
    const read = vi.fn(async () => listing);
    const covers = createFolderCovers(read);
    await covers.get({ ...file("a.mp3", "a"), parent_id: "d1" });
    await covers.get({ ...file("b.mp3", "b"), parent_id: "d2" });
    await covers.get({ ...file("c.mp3", "c"), parent_id: "d2" });
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("does not keep a failed read: the next track asks again", async () => {
    const read = vi.fn(async () => null);
    const covers = createFolderCovers(read);
    expect(await covers.get(file("a.mp3", "a"))).toBeNull();
    expect(await covers.get(file("b.mp3", "b"))).toBeNull();
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("keeps a good listing for five minutes, then reads the folder again, so a picture added later appears", async () => {
    let t = 1000;
    const read = vi.fn(async () => listing);
    const covers = createFolderCovers(read, () => t);
    await covers.get(file("a.mp3", "a"));
    t += LISTING_TTL_MS - 1;
    await covers.get(file("a.mp3", "a"));
    expect(read).toHaveBeenCalledTimes(1);
    t += 2;
    await covers.get(file("a.mp3", "a"));
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("forgets what it read when the person changes (reset)", async () => {
    const read = vi.fn(async () => listing);
    const covers = createFolderCovers(read);
    await covers.get(file("a.mp3", "a"));
    covers.reset();
    await covers.get(file("a.mp3", "a"));
    expect(read).toHaveBeenCalledTimes(2);
  });
});

describe("readFolder reads the folder a page at a time", () => {
  const page = (names: string[], next?: string) => ({ items: names.map((n) => file(n)), next_cursor: next });

  it("follows next_cursor to the end and is then complete", async () => {
    pages.list = [page(["01.mp3"], "1"), page(["02.mp3", "cover.jpg"])];
    pages.calls = [];
    pages.fail = false;
    const got = await readFolder(file("01.mp3"));
    expect(pages.calls).toEqual([undefined, "1"]);
    expect(got?.complete).toBe(true);
    expect(got?.items.map((n) => n.name)).toEqual(["01.mp3", "02.mp3", "cover.jpg"]);
  });

  it("stops at PAGES_MAX pages and is then not complete", async () => {
    pages.list = Array.from({ length: PAGES_MAX + 3 }, (_, i) => page([`${i}.mp3`], String(i + 1)));
    pages.calls = [];
    const got = await readFolder(file("x.mp3"));
    expect(pages.calls.length).toBe(PAGES_MAX);
    expect(got?.items.length).toBe(PAGES_MAX);
    expect(got?.complete).toBe(false);
  });

  it("stops early at a cover-named image, which no later page can beat", async () => {
    pages.list = [page(["01.mp3"], "1"), page(["cover.png"], "2"), page(["zz.mp3"])];
    pages.calls = [];
    const got = await readFolder(file("01.mp3"));
    expect(pages.calls).toEqual([undefined, "1"]);
    expect(got?.complete).toBe(false);
  });

  it("gives no listing when a page fails", async () => {
    pages.fail = true;
    expect(await readFolder(file("01.mp3"))).toBeNull();
    pages.fail = false;
  });
});
