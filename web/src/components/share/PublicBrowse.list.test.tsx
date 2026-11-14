// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's folder view (pb1): the visitor's listing is My files' table with type icons, a Download on every file
// that allows it, a view toggle and a sort kept per browser under the share's own key, and a path of buttons above the
// heading. No link is asked for before a click.

import { useLayoutEffect } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";
import { desktopViewport } from "../../test/viewport";

const api = vi.hoisted(() => ({
  visitorNode: vi.fn(),
  visitorChildren: vi.fn(),
  visitorLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

// The playlist is a stand-in: these checks are about the folder and which file opens, not about the player. It records
// the file it opened once, at its mount, as the heading checks' stand-in does.
const played = vi.hoisted(() => [] as string[]);
vi.mock("../files/MediaPlaylist", () => ({
  default: function StandIn({ start }: { start: Node }) {
    useLayoutEffect(() => {
      played.push(start.id);
    }, []);
    return <p>playlist of {start.name}</p>;
  },
}));

import PublicBrowse from "./PublicBrowse";

function dir(id: string, name: string, parent: string | null): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: parent,
    name,
    kind: "dir",
    size: 0,
    mime_type: "",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
  } as unknown as Node;
}

function file(id: string, name: string, over: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: "nod_root",
    name,
    kind: "file",
    size: 100,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
    ...over,
  } as unknown as Node;
}

const ROOT = dir("nod_root", "Plans", null);

// seed answers the root and its children; every node is known to the cache through the children answer.
function seed(children: Node[], root: Node = ROOT) {
  const byID = new Map<string, Node>([[root.id, root], ...children.map((c) => [c.id, c] as const)]);
  api.visitorNode.mockImplementation(async (_t: string, id: string) => {
    const n = byID.get(id);
    if (!n) throw new ApiError(404, "not_found", {}, 0);
    return n;
  });
  api.visitorChildren.mockImplementation(async (_t: string, id: string) => {
    if (id === root.id) return { items: children, next_cursor: null };
    return { items: [], next_cursor: null };
  });
}

const rowOf = (name: string) => screen.getByRole("row", { name });
const rowOrder = () => [...document.querySelectorAll("tbody tr[data-id]")].map((r) => r.getAttribute("aria-label"));

let undo: (() => void) | null = null;
let anchorClick: ReturnType<typeof vi.spyOn> | null = null;

beforeEach(() => {
  window.localStorage.clear();
  played.length = 0;
  undo = desktopViewport();
  // A download's anchor click would navigate jsdom; the check is about the link asked for, not the navigation.
  anchorClick = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
});

afterEach(() => {
  undo?.();
  undo = null;
  anchorClick?.mockRestore();
  anchorClick = null;
  vi.clearAllMocks();
  window.localStorage.clear();
});

describe("the visitor's folder is a table", () => {
  it("shows Name, Size and Modified with type icons, no owner columns, and asks for no link", async () => {
    seed([
      dir("nod_sub", "Notes", "nod_root"),
      file("nod_brief", "brief.txt"),
      file("nod_pic", "plan.png", { mime_type: "image/png", has_thumbnail: true }),
    ]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    expect(await screen.findByRole("columnheader", { name: /Name/ })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: /Size/ })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: /Modified/ })).toBeTruthy();
    expect(screen.queryByRole("columnheader", { name: "Your access" })).toBeNull();
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(rowOf("brief.txt").querySelector(".icon")).toBeTruthy();
    expect(rowOf("Notes").querySelector(".icon")).toBeTruthy();
    expect(rowOf("plan.png").querySelector("img")).toBeNull();
    expect(api.visitorLink).not.toHaveBeenCalled();
  });

  it("a file row's Download asks the attachment link only on its click", async () => {
    seed([file("nod_brief", "brief.txt")]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const button = await screen.findByRole("button", { name: "Download" });
    expect(api.visitorLink).not.toHaveBeenCalled();
    api.visitorLink.mockResolvedValue({ url: "https://content.example/brief", expires_at: "" });
    fireEvent.click(button);
    await vi.waitFor(() => expect(api.visitorLink).toHaveBeenCalledWith("tok_v", "nod_brief", "attachment"));
    expect(api.visitorLink).toHaveBeenCalledTimes(1);
  });

  it("has no Download on a folder or on a no-download file", async () => {
    seed([dir("nod_sub", "Notes", "nod_root"), file("nod_brief", "brief.txt"), file("nod_lock", "secret.txt", { no_download: true })]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    await screen.findByRole("row", { name: "brief.txt" });
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(1);
    expect(within(rowOf("Notes")).queryByRole("button", { name: "Download" })).toBeNull();
    expect(within(rowOf("secret.txt")).queryByRole("button", { name: "Download" })).toBeNull();
  });

  it("shows the downloads-off notice when the click is refused with download_restricted", async () => {
    seed([file("nod_lock", "secret.txt", { no_download: false })]);
    api.visitorLink.mockRejectedValue(new ApiError(403, "download_restricted", {}, 0));
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(await screen.findByText("Downloads of this file are turned off.")).toBeTruthy();
  });

  it("a click on the Size header reorders the rows, folders first", async () => {
    seed([file("nod_big", "a.bin", { size: 5000 }), dir("nod_sub", "Notes", "nod_root"), file("nod_small", "z.txt", { size: 10 })]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    await screen.findByRole("row", { name: "a.bin" });
    expect(rowOrder()).toEqual(["Notes", "a.bin", "z.txt"]);
    fireEvent.click(screen.getByRole("button", { name: "Size" }));
    expect(rowOrder()).toEqual(["Notes", "z.txt", "a.bin"]);
    fireEvent.click(screen.getByRole("button", { name: "Size" }));
    expect(rowOrder()).toEqual(["Notes", "a.bin", "z.txt"]);
  });

  it("Enter opens the focused row and the arrows move the focus", async () => {
    seed([dir("nod_sub", "Notes", "nod_root"), file("nod_brief", "brief.txt")]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const first = await screen.findByRole("row", { name: "Notes" });
    expect(first.getAttribute("tabindex")).toBe("0");
    fireEvent.keyDown(first, { key: "ArrowDown" });
    const second = rowOf("brief.txt");
    expect(second.getAttribute("tabindex")).toBe("0");
    fireEvent.keyDown(second, { key: "Enter" });
    expect(await screen.findByRole("heading", { name: "brief.txt" })).toBeTruthy();
  });
});

describe("the visitor's view and sort are kept per browser", () => {
  it("the view toggle shows tiles and is remembered under selfcloud.share.view, never the owner's key", async () => {
    seed([file("nod_brief", "brief.txt")]);
    const { unmount } = render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    const toggle = await screen.findByTestId("view-toggle");
    expect(toggle.getAttribute("data-view")).toBe("grid");
    fireEvent.click(toggle);
    expect(await screen.findByRole("listbox")).toBeTruthy();
    expect(screen.getByTestId("view-toggle").getAttribute("data-view")).toBe("list");
    const stored = JSON.parse(window.localStorage.getItem("selfcloud.share.view") ?? "{}") as { view: string };
    expect(stored.view).toBe("grid");
    expect(window.localStorage.getItem("selfcloud.files")).toBeNull();
    unmount();
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    expect(await screen.findByRole("listbox")).toBeTruthy();
  });

  it("a throwing localStorage still renders and toggles", async () => {
    seed([file("nod_brief", "brief.txt")]);
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage is blocked");
    });
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage is blocked");
    });
    try {
      render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
      expect(await screen.findByRole("row", { name: "brief.txt" })).toBeTruthy();
      fireEvent.click(screen.getByTestId("view-toggle"));
      expect(await screen.findByRole("listbox")).toBeTruthy();
    } finally {
      get.mockRestore();
      set.mockRestore();
    }
  });

  it("the Sort menu in tiles sorts the tiles", async () => {
    seed([file("nod_big", "a.bin", { size: 5000 }), dir("nod_sub", "Notes", "nod_root"), file("nod_small", "z.txt", { size: 10 })]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByTestId("view-toggle"));
    await screen.findByRole("listbox");
    expect(screen.queryByTestId("view-toggle")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Sort by" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Size" }));
    const tiles = () => [...document.querySelectorAll('[role="option"]')].map((r) => r.getAttribute("aria-label"));
    expect(tiles()).toEqual(["Notes", "z.txt", "a.bin"]);
    const stored = JSON.parse(window.localStorage.getItem("selfcloud.share.view") ?? "{}") as { sort: { key: string } };
    expect(stored.sort.key).toBe("size");
  });

  it("Play opens the first media file of the current order", async () => {
    seed([file("nod_clip", "clip.webm", { mime_type: "video/webm" }), file("nod_song", "song.mp3", { mime_type: "audio/mpeg" }), file("nod_brief", "brief.txt")]);
    api.visitorLink.mockResolvedValue({ url: "https://content.example/x", expires_at: "" });
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: "Play media" }));
    expect(await screen.findByRole("heading", { name: "clip.webm" })).toBeTruthy();
    expect(await screen.findByText("playlist of clip.webm")).toBeTruthy();
    expect(played).toEqual(["nod_clip"]);
    // Back to the folder, the other order: the last media file of the ascending order is first now.
    fireEvent.click(screen.getByRole("button", { name: "Plans" }));
    await screen.findByRole("row", { name: "clip.webm" });
    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    fireEvent.click(screen.getByRole("button", { name: "Play media" }));
    expect(await screen.findByRole("heading", { name: "song.mp3" })).toBeTruthy();
    expect(await screen.findByText("playlist of song.mp3")).toBeTruthy();
    expect(played).toEqual(["nod_clip", "nod_song"]);
  });

  it("has no Play when the folder has no media", async () => {
    seed([file("nod_brief", "brief.txt")]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    await screen.findByRole("row", { name: "brief.txt" });
    expect(screen.queryByRole("button", { name: "Play media" })).toBeNull();
  });
});

describe("the visitor's path and heading", () => {
  it("at the root there is no path line; in a subfolder the ancestors are buttons and the heading names the folder once", async () => {
    seed([dir("nod_sub", "Notes", "nod_root")]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    await screen.findByRole("row", { name: "Notes" });
    expect(screen.queryByRole("navigation", { name: "Path" })).toBeNull();
    fireEvent.click(rowOf("Notes"));
    const nav = await screen.findByRole("navigation", { name: "Path" });
    const crumb = within(nav).getByRole("button", { name: "Plans" });
    expect(within(nav).queryByText("Notes")).toBeNull();
    expect(screen.getAllByRole("heading", { name: "Notes" })).toHaveLength(1);
    fireEvent.click(crumb);
    expect(await screen.findByRole("heading", { name: "Plans" })).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "Path" })).toBeNull();
  });

  it("an empty folder keeps the empty state", async () => {
    seed([]);
    render(<PublicBrowse token="tok_v" root="nod_root" onlost={vi.fn()} />);
    expect(await screen.findByText("This folder is empty.")).toBeTruthy();
  });
});
