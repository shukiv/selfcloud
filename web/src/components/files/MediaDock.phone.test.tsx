// SPDX-License-Identifier: AGPL-3.0-or-later

// The session player at phone width (mp7): the album-page header (the album or the folder, the artist under it, Share only
// when the person may share), the queue as the table in the viewer, the action row, and the one element that survives the
// width change. Over 599 px nothing of this is drawn: the bar keeps its own transport and the drawer its panel.

import { act, configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { attachPage, startPlayback, stopPlayback } from "../../lib/files/playback";
import { Pager } from "../../lib/files/pager";
import { session, type Session } from "../../lib/session";
import { folders } from "../../lib/files/folders";
import { resetTags } from "../../lib/files/tags";
import type { Node } from "../../lib/files/types";
import MediaHost from "./MediaHost";

configure({ asyncUtilTimeout: 5000 });

const signedLink = vi.fn();
const startDownload = vi.fn();
const getResume = vi.fn();
const putResume = vi.fn();
const listSubtitles = vi.fn();
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  signedLink: (...args: unknown[]) => signedLink(...args),
  getResume: (...args: unknown[]) => getResume(...args),
  putResume: (...args: unknown[]) => putResume(...args),
  listSubtitles: (...args: unknown[]) => listSubtitles(...args),
}));
vi.mock("../../lib/files/ops", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/ops")>()),
  startDownload: (...args: unknown[]) => startDownload(...args),
}));

function file(name: string, extra: Partial<Node> = {}): Node {
  return {
    id: `n-${name}`,
    folder_id: "f1",
    parent_id: "d1",
    name,
    kind: "file",
    size: 10,
    mime_type: "audio/mpeg",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: true,
    place: "The Folder",
    ...extra,
  } as unknown as Node;
}

const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;

// installWidth makes matchMedia answer for a window of the given width, and lets a change of width reach every listener
// (the player's own hook and antd's breakpoints), as a real resize does.
function installWidth(px: number) {
  let width = px;
  const listeners = new Map<string, Set<() => void>>();
  const matches = (q: string) => {
    const max = /max-width:\s*(\d+)px/.exec(q);
    const min = /min-width:\s*(\d+)px/.exec(q);
    return max ? width <= Number(max[1]) : min ? width >= Number(min[1]) : false;
  };
  vi.stubGlobal("matchMedia", (q: string) => {
    let set = listeners.get(q);
    if (!set) {
      set = new Set();
      listeners.set(q, set);
    }
    const bag = set;
    return {
      get matches() {
        return matches(q);
      },
      media: q,
      onchange: null,
      addEventListener: (_: string, fn: () => void) => bag.add(fn),
      removeEventListener: (_: string, fn: () => void) => bag.delete(fn),
      addListener: (fn: () => void) => bag.add(fn),
      removeListener: (fn: () => void) => bag.delete(fn),
      dispatchEvent: () => false,
    };
  });
  return (w: number) => {
    width = w;
    for (const bag of listeners.values()) for (const fn of [...bag]) fn();
  };
}

const folderOf = (list: Node[]) => new Pager(list, null, async () => ({ items: [] }));

async function open(n: Node, list: Node[] = [n]) {
  act(() => startPlayback({ start: n, folder: folderOf(list) }));
  const el = await waitFor(() => {
    const a = document.querySelector("audio");
    if (!a) throw new Error("no audio yet");
    return a as HTMLAudioElement;
  });
  fireEvent.loadedMetadata(el);
  return el;
}

const draw = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <MediaHost />
      </MemoryRouter>
    </QueryClientProvider>,
  );

let play: ReturnType<typeof vi.spyOn>;
let pause: ReturnType<typeof vi.spyOn>;
let setWidth: (w: number) => void;

beforeAll(async () => {
  await import("./MediaDock");
}, 60000);

beforeEach(() => {
  signedLink.mockResolvedValue({ url: "https://content.example/a", disposition: "inline", expires_at: "" });
  getResume.mockResolvedValue({ position_seconds: 0, duration_seconds: 0 });
  putResume.mockResolvedValue(undefined);
  listSubtitles.mockResolvedValue([]);
  startDownload.mockResolvedValue(true);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  session.set(who("u1"));
  folders.set([]);
  resetTags();
  setWidth = installWidth(375);
});

afterEach(() => {
  stopPlayback();
  session.set(null);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  signedLink.mockReset();
  startDownload.mockReset();
  getResume.mockReset();
  putResume.mockReset();
  listSubtitles.mockReset();
  resetTags();
});

describe("the phone header", () => {
  it("names the album from the playing file's tags, with the artist under it", async () => {
    const n = file("song.mp3", { tags: { title: "The Title", artist: "The Artist", album: "The Album", duration_ms: 60000 } });
    draw();
    await open(n);
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).getByText("The Album")).toBeTruthy();
    expect(await within(head).findByText("by The Artist")).toBeTruthy();
  });

  it("falls back to the folder's name when the tags have no album, and says no by line without an artist", async () => {
    const n = file("song.mp3", { tags: { title: "The Title" } });
    draw();
    await open(n);
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).getByText("The Folder")).toBeTruthy();
    expect(within(head).queryByText(/^by /)).toBeNull();
  });

  it("falls back to the folder's name with no tags at all", async () => {
    draw();
    await open(file("plain.mp3"));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).getByText("The Folder")).toBeTruthy();
  });

  it("keeps the viewer's Close at the start", async () => {
    draw();
    await open(file("song.mp3"));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("has no back on the Media page, whose tab bar stays above", async () => {
    draw();
    await open(file("song.mp3"));
    await screen.findByTestId("media-phone-head");
    const stage = document.createElement("div");
    const queue = document.createElement("div");
    document.body.append(stage, queue);
    let detach: () => void = () => {};
    act(() => {
      detach = attachPage({ stage, queue });
    });
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).queryByRole("button", { name: "Close" })).toBeNull();
    expect(within(head).queryByRole("button", { name: "Stop and close" })).toBeNull();
    act(() => detach());
    stage.remove();
    queue.remove();
  });

  it("shows Share only when the person may share the playing file", async () => {
    draw();
    await open(file("song.mp3", { role: "editor", editors_can_share: true }));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).getByRole("button", { name: "Share" })).toBeTruthy();
  });

  it("shows no Share to a viewer", async () => {
    draw();
    await open(file("song.mp3", { role: "viewer", editors_can_share: true }));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).queryByRole("button", { name: "Share" })).toBeNull();
  });

  it("shows no Share to an editor where editors may not share", async () => {
    draw();
    await open(file("song.mp3", { role: "editor", editors_can_share: false }));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).queryByRole("button", { name: "Share" })).toBeNull();
  });

  it("shows no Share to an editor where the space keeps sharing for managers", async () => {
    folders.set([{ id: "f1", name: "Team", kind: "team", access: "write", role: "manager", members_only: false, sharing_managers_only: true, no_download_for_viewers: false, allow_links: true } as never]);
    draw();
    await open(file("song.mp3", { role: "editor", editors_can_share: true }));
    const head = await screen.findByTestId("media-phone-head");
    expect(within(head).queryByRole("button", { name: "Share" })).toBeNull();
  });
});

describe("the phone layout and the one element", () => {
  it("keeps the same element across the width change, playing on", async () => {
    draw();
    const el = await open(file("song.mp3"));
    (el as HTMLAudioElement & { __mark?: number }).__mark = 1;
    await screen.findByTestId("media-phone-head");
    const plays = play.mock.calls.length;
    act(() => setWidth(1280));
    await waitFor(() => expect(screen.queryByTestId("media-phone-head")).toBeNull());
    expect(document.querySelector("audio")).toBe(el);
    act(() => setWidth(375));
    await screen.findByTestId("media-phone-head");
    expect(document.querySelector("audio")).toBe(el);
    expect((el as HTMLAudioElement & { __mark?: number }).__mark).toBe(1);
    expect(el.isConnected).toBe(true);
    expect(play.mock.calls.length).toBe(plays);
    expect(document.querySelectorAll("audio").length).toBe(1);
  });

  it("draws the bar's own transport on desktop and not at phone width", async () => {
    draw();
    await open(file("song.mp3"));
    await screen.findByTestId("media-phone-head");
    const bar = screen.getByTestId("media-controls");
    expect(within(bar).queryByRole("button", { name: "Play" })).toBeNull();
    expect(within(bar).queryByRole("button", { name: "Previous file" })).toBeNull();
    expect(within(bar).queryByRole("button", { name: "Next file" })).toBeNull();
    expect(within(bar).queryByRole("button", { name: "Mute" })).toBeNull();
    const transport = screen.getByTestId("media-phone-transport");
    expect(within(transport).getByRole("button", { name: "Play" })).toBeTruthy();
    expect(within(transport).getByRole("button", { name: "Previous file" })).toBeTruthy();
    expect(within(transport).getByRole("button", { name: "Next file" })).toBeTruthy();
    act(() => setWidth(1280));
    await waitFor(() => expect(screen.queryByTestId("media-phone-transport")).toBeNull());
    const desktop = screen.getByTestId("media-controls");
    expect(within(desktop).getByRole("button", { name: "Play" })).toBeTruthy();
    expect(within(desktop).getByRole("button", { name: "Previous file" })).toBeTruthy();
    expect(within(desktop).getByRole("button", { name: "Next file" })).toBeTruthy();
    expect(within(desktop).getByRole("button", { name: "Mute" })).toBeTruthy();
  });

  it("draws the seek row at both widths, with the elapsed and the total", async () => {
    draw();
    const el = await open(file("song.mp3"));
    Object.defineProperty(el, "duration", { value: 65, configurable: true });
    fireEvent.durationchange(el);
    await screen.findByTestId("media-phone-head");
    const bar = screen.getByTestId("media-controls");
    expect(bar.querySelector(".media-seek-row")).toBeTruthy();
    expect(within(bar).getByTestId("media-time").textContent).toBe("0:00");
    act(() => setWidth(1280));
    await waitFor(() => expect(screen.queryByTestId("media-phone-head")).toBeNull());
    expect(screen.getByTestId("media-controls").querySelector(".media-seek-row")).toBeTruthy();
  });

  it("shows the queue as the table in the viewer at phone width, and the drawer's panel on desktop", async () => {
    const a = file("a.mp3");
    const b = file("b.mp3");
    draw();
    await open(a, [a, b]);
    await screen.findByTestId("media-phone-head");
    const table = await screen.findByTestId("media-queue-table");
    const drawer = table.closest(".media-drawer") as HTMLElement;
    expect(drawer).toBeTruthy();
    expect(drawer.hasAttribute("inert")).toBe(false);
    expect(drawer.classList.contains("media-drawer--closed")).toBe(false);
    expect(screen.queryByTestId("media-queue")).toBeNull();
    act(() => setWidth(1280));
    await waitFor(() => expect(screen.queryByTestId("media-queue-table")).toBeNull());
    expect(screen.getByTestId("media-queue")).toBeTruthy();
  });

  it("marks the playing row and plays a tapped row through the one player", async () => {
    const a = file("a.mp3");
    const b = file("b.mp3");
    draw();
    await open(a, [a, b]);
    const table = await screen.findByTestId("media-queue-table");
    const rows = within(table).getAllByTestId("media-qrow");
    expect(rows[0].classList.contains("media-qrow--on")).toBe(true);
    fireEvent.click(within(rows[1]).getByText("b.mp3"));
    await waitFor(() => expect(document.querySelector("audio")?.getAttribute("aria-label")).toBe("b.mp3"));
    expect(document.querySelectorAll("audio").length).toBe(1);
  });

  it("draws the line with the playing row's place in the queue", async () => {
    const a = file("a.mp3", { tags: { title: "First", artist: "A" } });
    const b = file("b.mp3");
    draw();
    await open(a, [a, b]);
    expect(await screen.findByTestId("media-phone-line")).toHaveTextContent("1. A - First");
  });
});

describe("the phone action row in the session", () => {
  it("has Playlist, Download and More, and Download downloads the playing file", async () => {
    draw();
    await open(file("song.mp3"));
    const row = await screen.findByTestId("media-phone-actions");
    fireEvent.click(within(row).getByRole("button", { name: "Download" }));
    await waitFor(() => expect(startDownload).toHaveBeenCalledWith("n-song.mp3"));
    expect(within(row).getByRole("button", { name: "Playlist" })).toBeTruthy();
    expect(within(row).getByRole("button", { name: "More" })).toBeTruthy();
  });

  it("hides Download from a person the download rule refuses", async () => {
    draw();
    await open(file("song.mp3", { role: "viewer", no_download: true }));
    const row = await screen.findByTestId("media-phone-actions");
    expect(within(row).queryByRole("button", { name: "Download" })).toBeNull();
  });

  it("Playlist opens the saved-playlists flow the queue panel has", async () => {
    draw();
    await open(file("song.mp3"));
    const row = await screen.findByTestId("media-phone-actions");
    fireEvent.click(within(row).getByRole("button", { name: "Playlist" }));
    expect(await screen.findByRole("button", { name: "Save queue" })).toBeTruthy();
    expect(screen.getByText("Saved playlists")).toBeTruthy();
  });

  it("keeps no phone part over 599 px: the desktop head and the drawer stay as they were", async () => {
    setWidth(1280);
    draw();
    await open(file("song.mp3"));
    await waitFor(() => expect(document.querySelector("audio")).toBeTruthy());
    expect(screen.queryByTestId("media-phone-head")).toBeNull();
    expect(screen.queryByTestId("media-phone-actions")).toBeNull();
    expect(screen.queryByTestId("media-phone-line")).toBeNull();
    expect(screen.getByTestId("media-count")).toBeTruthy();
    expect(screen.getByTestId("media-queue")).toBeTruthy();
  });
});