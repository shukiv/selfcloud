// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's queue is a table like the drive's other lists (mp1b): rows numbered in play order, the file on screen
// marked, the tags' title, artist, cover and length, and the row actions Play, Move up, Move down and Remove. The shared
// head (Shuffle, Repeat, Add files), the capped note, the loading-more status, the add panel and the saved section are the
// drawer panel's own parts.

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetTags } from "../../lib/files/tags";
import { watchThumbnail } from "../../lib/files/thumbnail";
import type { Entry, Mark } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import { session } from "../../lib/session";
import type { MediaQueueProps } from "./MediaQueue";
import MediaQueueTable from "./MediaQueueTable";

// The cover is asked for through watchThumbnail. The spy records which watches were started; the real one runs.
vi.mock("../../lib/files/thumbnail", async (orig) => {
  const real = await orig<typeof import("../../lib/files/thumbnail")>();
  return { ...real, watchThumbnail: vi.fn(real.watchThumbnail) };
});

function node(name: string, mime: string, extra: Partial<Node> = {}): Node {
  return { id: `nod_${name}`, folder_id: "f", parent_id: "d", name, kind: "file", mime_type: mime, size: 1, created_at: "", updated_at: "", ...extra } as unknown as Node;
}

function entry(n: Node, mark: Mark = "ok"): Entry {
  return { node: n, mark };
}

// viewport is a desktop window with a mouse, so the table shows its secondary column and the row's first three icons
// inline, the rest in the ⋮ menu (the shared preset list's rule).
function viewport(px: number, coarse = false) {
  vi.stubGlobal("matchMedia", (query: string) => {
    const m = /min-width: (\d+)px/.exec(query);
    const matches = m ? px >= Number(m[1]) : coarse && query === "(pointer: coarse)";
    return { matches, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  });
}

function props(entries: Entry[], over: Partial<MediaQueueProps> = {}): MediaQueueProps {
  return {
    entries,
    currentId: null,
    repeat: "off",
    shuffle: false,
    capped: false,
    moreLeft: false,
    busy: false,
    candidates: [],
    adding: false,
    onAddOpen: vi.fn(),
    onAdd: vi.fn(),
    onLoadMore: vi.fn(),
    onPlay: vi.fn(),
    onRemove: vi.fn(),
    onMove: vi.fn(),
    onShuffle: vi.fn(),
    onRepeat: vi.fn(),
    ...over,
  };
}

const rows = () => screen.getAllByTestId("media-qrow");
const cells = (row: HTMLElement) => row.querySelectorAll("td");

beforeEach(() => {
  resetTags();
  vi.mocked(watchThumbnail).mockClear();
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
  viewport(1200);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("MediaQueueTable: the queue as a table", () => {
  it("is the table section, with the queue's head and the columns Name, # and Length", async () => {
    const p = props([entry(node("a.ogg", "audio/ogg"))]);
    const { container } = render(<MediaQueueTable {...p} />);
    const section = container.querySelector('[data-testid="media-queue-table"]') as HTMLElement;
    expect(section.classList.contains("media-queue")).toBe(true);
    expect(section.classList.contains("media-queue--table")).toBe(true);
    expect(screen.getByRole("region", { name: "Playlist" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Shuffle" })).toBeTruthy();
    // The media catalogue's Length word arrives with its lazy load.
    expect(await screen.findByText("Length")).toBeTruthy();
    expect(screen.getByText("Name")).toBeTruthy();
    expect(screen.getByText("#")).toBeTruthy();
  });

  it("numbers the rows 1..n in queue order", async () => {
    render(<MediaQueueTable {...props([entry(node("a.ogg", "audio/ogg")), entry(node("b.ogg", "audio/ogg")), entry(node("c.ogg", "audio/ogg"))])} />);
    await screen.findByText("Length");
    const rs = rows();
    expect(rs).toHaveLength(3);
    expect(cells(rs[0])[1].textContent).toBe("1");
    expect(cells(rs[1])[1].textContent).toBe("2");
    expect(cells(rs[2])[1].textContent).toBe("3");
    expect(rs[0].getAttribute("aria-label")).toBe("a.ogg");
    expect(rs[2].getAttribute("aria-label")).toBe("c.ogg");
  });

  it("marks the current row and says Playing, or Paused when the file is not playing", async () => {
    const entries = [entry(node("a.ogg", "audio/ogg")), entry(node("b.ogg", "audio/ogg"))];
    const { rerender } = render(<MediaQueueTable {...props(entries, { currentId: "nod_a.ogg", playing: true })} />);
    await screen.findByText("Length");
    expect(rows()[0].classList.contains("media-qrow--on")).toBe(true);
    expect(rows()[1].classList.contains("media-qrow--on")).toBe(false);
    expect(within(rows()[0]).getByText("Playing")).toBeTruthy();
    rerender(<MediaQueueTable {...props(entries, { currentId: "nod_a.ogg", playing: false })} />);
    expect(within(rows()[0]).getByText("Paused")).toBeTruthy();
    expect(within(rows()[0]).queryByText("Playing")).toBeNull();
  });

  it("a click and Enter on a row play it", async () => {
    const onPlay = vi.fn();
    render(<MediaQueueTable {...props([entry(node("a.ogg", "audio/ogg")), entry(node("b.ogg", "audio/ogg"))], { onPlay })} />);
    await screen.findByText("Length");
    fireEvent.click(rows()[1]);
    expect(onPlay).toHaveBeenCalledWith("nod_b.ogg");
    fireEvent.focus(rows()[0]);
    fireEvent.keyDown(rows()[0], { key: "Enter" });
    expect(onPlay).toHaveBeenCalledWith("nod_a.ogg");
    expect(onPlay).toHaveBeenCalledTimes(2);
  });

  it("a restricted or unavailable row does not play, and its Play is disabled", async () => {
    const onPlay = vi.fn();
    const entries = [entry(node("a.ogg", "audio/ogg")), entry(node("b.ogg", "audio/ogg"), "restricted"), entry(node("c.ogg", "audio/ogg"), "unavailable")];
    render(<MediaQueueTable {...props(entries, { onPlay })} />);
    await screen.findByText("Length");
    const restricted = rows()[1];
    const unavailable = rows()[2];
    expect(restricted.classList.contains("media-qrow--off")).toBe(true);
    expect(unavailable.classList.contains("media-qrow--off")).toBe(true);
    // The unavailable row says Unavailable in place of its name.
    expect(within(unavailable).getByTestId("media-unavailable").textContent).toBe("Unavailable");
    for (const row of [restricted, unavailable]) {
      const play = within(row).getByRole("button", { name: /^Play / });
      expect(play.getAttribute("aria-disabled")).toBe("true");
      fireEvent.click(play);
      fireEvent.click(row);
      fireEvent.focus(row);
      fireEvent.keyDown(row, { key: "Enter" });
    }
    expect(onPlay).not.toHaveBeenCalled();
    // The restricted row says so; a failed row says it can't play.
    expect(within(restricted).getByText("Not allowed: no download")).toBeTruthy();
  });

  it("a failed row says it can't play, and still plays on a click", async () => {
    const onPlay = vi.fn();
    render(<MediaQueueTable {...props([entry(node("a.ogg", "audio/ogg"), "failed")], { onPlay })} />);
    await screen.findByText("Length");
    expect(within(rows()[0]).getByText("Can't play")).toBeTruthy();
    fireEvent.click(rows()[0]);
    expect(onPlay).toHaveBeenCalledWith("nod_a.ogg");
  });

  it("Move up is disabled on the first row and Move down on the last; each action calls its handler", async () => {
    const onMove = vi.fn();
    const onPlay = vi.fn();
    const onRemove = vi.fn();
    const entries = [entry(node("a.ogg", "audio/ogg")), entry(node("b.ogg", "audio/ogg")), entry(node("c.ogg", "audio/ogg"))];
    render(<MediaQueueTable {...props(entries, { onMove, onPlay, onRemove })} />);
    await screen.findByText("Length");
    const [first, middle, last] = rows();
    expect(within(first).getByRole("button", { name: "Move a.ogg up" }).getAttribute("aria-disabled")).toBe("true");
    expect(within(last).getByRole("button", { name: "Move c.ogg down" }).getAttribute("aria-disabled")).toBe("true");
    expect(within(first).getByRole("button", { name: "Move a.ogg down" }).getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(within(middle).getByRole("button", { name: "Move b.ogg up" }));
    expect(onMove).toHaveBeenCalledWith("nod_b.ogg", -1);
    fireEvent.click(within(middle).getByRole("button", { name: "Move b.ogg down" }));
    expect(onMove).toHaveBeenCalledWith("nod_b.ogg", 1);
    fireEvent.click(within(first).getByRole("button", { name: "Play a.ogg" }));
    expect(onPlay).toHaveBeenCalledWith("nod_a.ogg");
    // Remove is the fourth action, in the row's ⋮ menu on a desktop.
    fireEvent.click(within(first).getByRole("button", { name: "More actions for a.ogg" }));
    const item = await screen.findByRole("menuitem", { name: "Remove a.ogg from the playlist" });
    fireEvent.click(item);
    expect(onRemove).toHaveBeenCalledWith("nod_a.ogg");
  });

  it("Shuffle, Repeat and Add files call their handlers and show their pressed and expanded state", async () => {
    const onShuffle = vi.fn();
    const onRepeat = vi.fn();
    const onAddOpen = vi.fn();
    const p = props([entry(node("a.ogg", "audio/ogg"))], { shuffle: true, repeat: "all", adding: true, onShuffle, onRepeat, onAddOpen });
    render(<MediaQueueTable {...p} />);
    await screen.findByText("Length");
    const shuffle = screen.getByRole("button", { name: "Shuffle" });
    const repeat = screen.getByRole("button", { name: "Repeat: Repeat all" });
    const add = screen.getByRole("button", { name: "Add files" });
    expect(shuffle.getAttribute("aria-pressed")).toBe("true");
    expect(repeat.getAttribute("aria-pressed")).toBe("true");
    expect(add.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(shuffle);
    fireEvent.click(repeat);
    fireEvent.click(add);
    expect(onShuffle).toHaveBeenCalledTimes(1);
    expect(onRepeat).toHaveBeenCalledTimes(1);
    expect(onAddOpen).toHaveBeenCalledTimes(1);
    // The add panel is the drawer's own part.
    expect(screen.getByTestId("media-add")).toBeTruthy();
  });

  it("capped shows the Alert", async () => {
    render(<MediaQueueTable {...props([entry(node("a.ogg", "audio/ogg"))], { capped: true })} />);
    await screen.findByText("Length");
    expect(screen.getByText(/holds up to/)).toBeTruthy();
  });

  it("an empty queue shows the empty state inside the table, under the header row", async () => {
    const { container } = render(<MediaQueueTable {...props([])} />);
    expect(await screen.findByText("Nothing in the playlist.")).toBeTruthy();
    const table = container.querySelector('[data-testid="media-queue-table"] table') as HTMLElement;
    // The header row stays, with the column names; the empty state is the table's body.
    expect(table.querySelector("thead")).not.toBeNull();
    expect(within(table).getByText("Name")).toBeTruthy();
    expect(within(table).getByText("#")).toBeTruthy();
    const empty = table.querySelector(".media-queue-empty") as HTMLElement;
    expect(empty).not.toBeNull();
    expect(empty.closest("tbody")).not.toBeNull();
    expect(screen.queryByTestId("media-qrow")).toBeNull();
  });

  it("an audio row shows its cover and its length; past an hour the length is h:mm:ss; no duration is an em dash; a video row the video box", async () => {
    const entries = [
      entry(node("a.mp3", "audio/mpeg", { has_thumbnail: true, tags: { title: "Song", artist: "Artist", duration_ms: 187000 } })),
      entry(node("long.ogg", "audio/ogg", { tags: { duration_ms: 3723000 } })),
      entry(node("plain.ogg", "audio/ogg")),
      entry(node("v.webm", "video/webm", { has_thumbnail: true })),
    ];
    render(<MediaQueueTable {...props(entries)} />);
    expect(await screen.findByText("3:07")).toBeTruthy();
    const [a, long, plain, video] = rows();
    // The cover box is 40 px, the audio kind, and asked for only where the file is known to have one.
    const box = within(a).getByTestId("tag-cover");
    expect(box.style.width).toBe("40px");
    expect(box.dataset.kind).toBe("audio");
    expect(within(a).getByText("Song")).toBeTruthy();
    expect(within(a).getByText("Artist")).toBeTruthy();
    expect(cells(a)[2].textContent).toBe("3:07");
    expect(cells(long)[2].textContent).toBe("1:02:03");
    // No duration: an em dash, with no screen-reader text.
    expect(cells(plain)[2].textContent).toBe("—");
    expect(cells(plain)[2].querySelector(".sr-only")).toBeNull();
    expect(cells(video)[2].textContent).toBe("—");
    expect(within(video).getByTestId("tag-cover").dataset.kind).toBe("video");
    expect(watchThumbnail).toHaveBeenCalledTimes(1);
  });

  it("the saved node is drawn under the list", async () => {
    render(<MediaQueueTable {...props([entry(node("a.ogg", "audio/ogg"))], { saved: <div data-testid="saved-node">Saved playlists</div> })} />);
    await screen.findByText("Length");
    expect(screen.getByTestId("saved-node")).toBeTruthy();
  });

  it("asks for the audio files' tags only, not a video's", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ items: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    act(() => session.set({ user: { id: "u", email: "u@x.test" }, account: null, csrf: "c", publicLinks: false } as never));
    render(<MediaQueueTable {...props([entry(node("a.mp3", "audio/mpeg")), entry(node("v.webm", "video/webm"))])} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const url = String((fetchMock.mock.calls as unknown as unknown[][])[0][0]);
    expect(url).toContain("/api/v1/media/tags?ids=nod_a.mp3");
    expect(url).not.toContain("v.webm");
    act(() => session.set(null));
  });
});
