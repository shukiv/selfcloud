// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's More menu: one icon for both kinds of file, its popup in the full-screen stage, the A-B loop that
// belongs to the file, and the sleep timer whose store outlives the element. The menu never makes a second element.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Pager } from "../../lib/files/pager";
import type { Node } from "../../lib/files/types";
import { setMedia, startPlayback, stopPlayback } from "../../lib/files/playback";
import { getSleep, setSleep, stopsAtEnd } from "../../lib/files/sleepTimer";
import MediaControls from "./MediaControls";
import MediaExtras from "./MediaExtras";
import MediaPlayer from "./MediaPlayer";
import MediaPlaylist from "./MediaPlaylist";

const SRC = "https://content.example/a.ogg";
const LINK = { url: SRC };

// el is a fake media element, as the other media tests build one: jsdom loads nothing, so the source and the place
// are what the test says they are.
function el(kind: "audio" | "video" = "audio"): HTMLMediaElement {
  const m = document.createElement(kind);
  Object.defineProperty(m, "currentSrc", { value: SRC, writable: true, configurable: true });
  return m;
}

function at(m: HTMLMediaElement, seconds: number) {
  Object.defineProperty(m, "currentTime", { value: seconds, writable: true, configurable: true });
}

function timeupdate(m: HTMLMediaElement) {
  act(() => {
    m.dispatchEvent(new Event("timeupdate"));
  });
}

function defineDoc(prop: string, value: unknown) {
  Object.defineProperty(document, prop, { configurable: true, get: () => value });
}

const host = () => document.body;

function draw(m: HTMLMediaElement) {
  return render(
    <App>
      <MediaExtras media={m} popupHost={host} />
    </App>,
  );
}

async function openMenu() {
  fireEvent.click(screen.getByRole("button", { name: "More options" }));
  // The menu is open once the A-B loop submenu title is on screen. A submenu title's accessible name carries its arrow
  // ("A-B loop right"), so it is found by its text, as the sidebar's New menu is.
  return screen.findByText("A-B loop");
}

// The submenus open on hover, as antd's do; a mouseover is what the browser sends for it.
async function openSub(label: string) {
  const title = (await screen.findByText(label)).closest('[role="menuitem"]') as HTMLElement;
  fireEvent.mouseOver(title);
}

async function pick(name: string | RegExp) {
  fireEvent.click(await screen.findByRole("menuitem", { name }));
}

const file = (name: string): Node =>
  ({ id: name, folder_id: "f", parent_id: "d", name, kind: "file", size: 1, mime_type: "video/webm", created_at: "", modified_at: "", role: "editor", no_download: false, editors_can_share: false }) as unknown as Node;

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
});

afterEach(() => {
  setSleep("off");
  stopPlayback();
  vi.useRealTimers();
  vi.restoreAllMocks();
  delete (document as unknown as Record<string, unknown>).fullscreenElement;
});

describe("the More button and its menu", () => {
  it.each(["audio", "video"] as const)("draws one labelled icon for %s, with the loop and the sleep timer in its menu", async (kind) => {
    draw(el(kind));
    const button = screen.getByRole("button", { name: "More options" });
    expect(button.className).toContain("media-btn");
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    await openMenu();
    expect(await screen.findByText("A-B loop")).toBeTruthy();
    expect(await screen.findByText("Sleep timer")).toBeTruthy();
  });

  it("opens its popup in the stage while the stage is full screen, and on the page otherwise", async () => {
    const stage = document.createElement("div");
    document.body.appendChild(stage);
    const { unmount } = render(<MediaControls media={el("video")} kind="video" stage={stage} />);
    defineDoc("fullscreenElement", stage);
    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    const menu = await screen.findByRole("menu");
    expect(stage.contains(menu)).toBe(true);
    unmount();
    stage.remove();

    const page = render(<MediaControls media={el("video")} kind="video" stage={null} />);
    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    const onPage = await screen.findByRole("menu");
    expect(document.body.contains(onPage)).toBe(true);
    page.unmount();
  });

  it("makes no second element and remounts no player when its items are used", async () => {
    const { container } = render(<MediaPlayer kind="audio" name="song.mp3" link={LINK} mint={async () => LINK} onFail={() => {}} />);
    const first = container.querySelector("audio") as HTMLAudioElement;
    (first as HTMLAudioElement & { __mark?: number }).__mark = 1;
    await openMenu();
    await openSub("Sleep timer");
    await pick("15 minutes");
    expect(container.querySelectorAll("audio").length).toBe(1);
    expect(container.querySelector("audio")).toBe(first);
    expect((first as HTMLAudioElement & { __mark?: number }).__mark).toBe(1);
    expect(getSleep().choice).toBe(15);
  });
});

describe("the A-B loop", () => {
  it("shows its state: off, A alone, then the range, with the times as the bar writes them", async () => {
    const m = el();
    draw(m);
    at(m, 62);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop off")).toBeTruthy();
    await pick("Set start (A)");
    at(m, 75);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("A 1:02")).toBeTruthy();
    await pick("Set end (B)");
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop 1:02–1:15")).toBeTruthy();
  });

  it("seeks to A on every timeupdate at or past B", async () => {
    const m = el();
    draw(m);
    at(m, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(m, 20);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    at(m, 20);
    timeupdate(m);
    expect(m.currentTime).toBe(10);
    // Below B nothing happens; at B and past it the place returns to A.
    at(m, 19);
    timeupdate(m);
    expect(m.currentTime).toBe(19);
    at(m, 25);
    timeupdate(m);
    expect(m.currentTime).toBe(10);
  });

  it("refuses a B within half a second of A with the note, and sets nothing", async () => {
    const m = el();
    draw(m);
    at(m, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(m, 10.4);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    expect(await screen.findByText("The end must be at least 0.5 seconds after the start.")).toBeTruthy();
    // The loop is still A alone: no seek at the refused place, and the state says so.
    timeupdate(m);
    expect(m.currentTime).toBe(10.4);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("A 0:10")).toBeTruthy();
  });

  it("keeps Set end (B) disabled until A is set", async () => {
    draw(el());
    await openMenu();
    await openSub("A-B loop");
    const b = await screen.findByRole("menuitem", { name: "Set end (B)" });
    expect(b.getAttribute("aria-disabled")).toBe("true");
    expect((await screen.findByRole("menuitem", { name: "Clear loop" })).getAttribute("aria-disabled")).toBe("true");
  });

  it("ends the loop on Clear", async () => {
    const m = el();
    draw(m);
    at(m, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(m, 20);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    await openMenu();
    await openSub("A-B loop");
    await pick("Clear loop");
    at(m, 25);
    timeupdate(m);
    expect(m.currentTime).toBe(25);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop off")).toBeTruthy();
  });

  it("keeps the loop when the person seeks outside A..B: reaching B still returns to A", async () => {
    const m = el();
    draw(m);
    at(m, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(m, 20);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    at(m, 5);
    timeupdate(m);
    expect(m.currentTime).toBe(5);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop 0:10–0:20")).toBeTruthy();
  });

  it("clears the loop when the element's currentSrc changes to another file", async () => {
    const m = el();
    draw(m);
    at(m, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(m, 20);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    Object.defineProperty(m, "currentSrc", { value: "https://content.example/b.ogg", writable: true, configurable: true });
    timeupdate(m);
    at(m, 25);
    timeupdate(m);
    expect(m.currentTime).toBe(25);
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop off")).toBeTruthy();
  });

  it("clears the loop when a new element takes the player", async () => {
    const first = el();
    const { rerender } = draw(first);
    at(first, 10);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set start (A)");
    at(first, 20);
    await openMenu();
    await openSub("A-B loop");
    await pick("Set end (B)");
    const second = el();
    rerender(
      <App>
        <MediaExtras media={second} popupHost={host} />
      </App>,
    );
    await openMenu();
    await openSub("A-B loop");
    expect(await screen.findByText("Loop off")).toBeTruthy();
    // The old element's listener is gone with the loop.
    at(first, 25);
    timeupdate(first);
    expect(first.currentTime).toBe(25);
  });
});

describe("the sleep timer's menu", () => {
  it("checks the chosen one, Off at first", async () => {
    draw(el());
    await openMenu();
    await openSub("Sleep timer");
    const off = await screen.findByRole("menuitem", { name: "Off" });
    expect(off.className).toContain("ant-dropdown-menu-item-selected");
    await pick("30 minutes");
    expect(getSleep().choice).toBe(30);
    await openMenu();
    await openSub("Sleep timer");
    const chosen = await screen.findByRole("menuitem", { name: "30 minutes" });
    expect(chosen.className).toContain("ant-dropdown-menu-item-selected");
    expect((await screen.findByRole("menuitem", { name: "Off" })).className).not.toContain("ant-dropdown-menu-item-selected");
  });

  it("shows the time left while a timed choice runs, counts it down, and pauses the element at the end", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const node = file("a.ogg");
    startPlayback({ start: node, folder: new Pager([node], null, async () => ({ items: [] })) });
    const m = el();
    const pause = vi.spyOn(m, "pause").mockImplementation(() => {});
    setMedia(m);
    draw(m);
    act(() => {
      setSleep(15);
    });
    await openMenu();
    await openSub("Sleep timer");
    expect(await screen.findByText("Stops in 15:00")).toBeTruthy();
    await act(async () => {
      vi.advanceTimersByTime(61_000);
    });
    expect(await screen.findByText("Stops in 13:59")).toBeTruthy();
    await act(async () => {
      vi.advanceTimersByTime(14 * 60_000);
    });
    expect(pause).toHaveBeenCalledTimes(1);
    expect(getSleep().choice).toBe("off");
    await waitFor(() => expect(screen.queryByText(/Stops in/)).toBeNull());
    expect((await screen.findByRole("menuitem", { name: "Off" })).className).toContain("ant-dropdown-menu-item-selected");
  });
});

describe("End of this file in the playlist", () => {
  it("makes stopsAtEnd true once, keeps the ended file on screen, and spends the choice", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const fetchLink = vi.fn(async (n: Node) => ({ url: `https://content.example/${n.id}` }));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    const playing = () => container.querySelector("video")?.getAttribute("src") ?? null;
    expect(playing()).toBe("https://content.example/a.webm");
    setSleep("end");
    expect(stopsAtEnd()).toBe(true);
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    // The queue does not move: the same file stays on screen, ended, and the choice is Off again.
    expect(playing()).toBe("https://content.example/a.webm");
    expect(fetchLink).toHaveBeenCalledTimes(1);
    expect(stopsAtEnd()).toBe(false);
    expect(getSleep().choice).toBe("off");
    // With the choice Off the end advances, as it always did.
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(playing()).toBe("https://content.example/b.webm");
  });

  it("advances at the end when the choice is Off", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const fetchLink = vi.fn(async (n: Node) => ({ url: `https://content.example/${n.id}` }));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    setSleep("off");
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(container.querySelector("video")?.getAttribute("src")).toBe("https://content.example/b.webm");
  });
});
