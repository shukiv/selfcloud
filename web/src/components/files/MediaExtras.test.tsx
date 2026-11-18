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
import { PRESETS, writeEq } from "../../lib/files/equalizer";
import { session, type Session } from "../../lib/session";

// The Web Audio graph is mocked: jsdom has no AudioContext, and the panel's clicks and the kept choice's play event
// are judged by what they ask of the graph, not by sound.
const eqGraph = vi.hoisted(() => ({
  attach: vi.fn(async (_m: HTMLMediaElement): Promise<boolean> => true),
  setGains: vi.fn((_m: HTMLMediaElement, _gains: number[]) => {}),
}));
vi.mock("../../lib/files/eqGraph", () => eqGraph);
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

// The Picture panel's sliders move by their step on the arrow keys, as antd's do: Up and Right add one step.
const UP = 38;
const DOWN = 40;

async function openPicture() {
  await openMenu();
  fireEvent.click(await screen.findByRole("menuitem", { name: "Picture…" }));
  return screen.findByRole("slider", { name: "Brightness" });
}

describe("the Picture panel", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    window.localStorage.clear();
    session.set(null);
  });

  it("offers Picture… on a video only, and never styles an audio element", async () => {
    const m = el("audio");
    const audio = draw(m);
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Picture…" })).toBeNull();
    expect(m.style.filter).toBe("");
    expect(m.style.objectFit).toBe("");
    audio.unmount();

    draw(el("video"));
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Picture…" })).toBeTruthy();
  });

  it("moving Brightness sets the element's filter at once, shows the value, and keeps it", async () => {
    const m = el("video");
    draw(m);
    const handle = await openPicture();
    for (let i = 0; i < 4; i++) fireEvent.keyDown(handle, { keyCode: UP });
    expect(m.style.filter).toBe("brightness(120%) contrast(100%) saturate(100%)");
    expect(screen.getByText("120%")).toBeTruthy();
    expect(JSON.parse(window.localStorage.getItem("selfcloud.picture.visitor") ?? "{}").brightness).toBe(120);
  });

  it("Fit sets the element's objectFit: Fit, Fill and Stretch", async () => {
    const m = el("video");
    draw(m);
    await openPicture();
    fireEvent.click(await screen.findByRole("radio", { name: "Fill" }));
    expect(m.style.objectFit).toBe("cover");
    fireEvent.click(screen.getByRole("radio", { name: "Stretch" }));
    expect(m.style.objectFit).toBe("fill");
    fireEvent.click(screen.getByRole("radio", { name: "Fit" }));
    expect(m.style.objectFit).toBe("contain");
  });

  it("Reset returns to the default and keeps it", async () => {
    const m = el("video");
    draw(m);
    const handle = await openPicture();
    fireEvent.keyDown(handle, { keyCode: UP });
    fireEvent.click(screen.getByRole("radio", { name: "Fill" }));
    expect(m.style.filter).not.toBe("none");
    expect(m.style.objectFit).toBe("cover");
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(m.style.filter).toBe("none");
    expect(m.style.objectFit).toBe("contain");
    expect(JSON.parse(window.localStorage.getItem("selfcloud.picture.visitor") ?? "{}")).toEqual({
      fit: "contain",
      brightness: 100,
      contrast: 100,
      saturation: 100,
    });
  });

  it("a new element takes the kept values, and the old one is left plain", async () => {
    const first = el("video");
    const { rerender } = draw(first);
    await openPicture();
    const handle = await screen.findByRole("slider", { name: "Contrast" });
    fireEvent.keyDown(handle, { keyCode: DOWN });
    expect(first.style.filter).toBe("brightness(100%) contrast(95%) saturate(100%)");
    const second = el("video");
    rerender(
      <App>
        <MediaExtras media={second} popupHost={host} />
      </App>,
    );
    await waitFor(() => expect(second.style.filter).toBe("brightness(100%) contrast(95%) saturate(100%)"));
    expect(second.style.objectFit).toBe("contain");
    expect(first.style.filter).toBe("");
    expect(first.style.objectFit).toBe("");
  });

  it("keeps the choice under the signed-in person's own key", async () => {
    session.set({ user: { id: "usr_pic" } } as unknown as Session);
    const m = el("video");
    draw(m);
    const handle = await openPicture();
    fireEvent.keyDown(handle, { keyCode: UP });
    expect(window.localStorage.getItem("selfcloud.picture.usr_pic")).not.toBeNull();
    expect(window.localStorage.getItem("selfcloud.picture.visitor")).toBeNull();
    expect(m.style.filter).toBe("brightness(105%) contrast(100%) saturate(100%)");
  });
});

describe("the Audio track submenu", () => {
  interface Track {
    label: string;
    language: string;
    enabled: boolean;
  }

  function withTracks(m: HTMLMediaElement, list: Track[]) {
    Object.defineProperty(m, "audioTracks", { value: list, configurable: true, writable: true });
    return list;
  }

  const track = (over: Partial<Track> = {}): Track => ({ label: "", language: "", enabled: false, ...over });

  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    window.localStorage.clear();
  });

  it("stays hidden without audioTracks and with one track", async () => {
    const none = draw(el("video"));
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Audio track" })).toBeNull();
    none.unmount();

    const one = el("video");
    withTracks(one, [track({ label: "English", enabled: true })]);
    draw(one);
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Audio track" })).toBeNull();
  });

  it("shows the labels, else the language, else Track n, with the enabled one checked", async () => {
    const m = el("video");
    withTracks(m, [track({ label: "English", language: "en", enabled: true }), track({ language: "fr" }), track()]);
    draw(m);
    await openMenu();
    await openSub("Audio track");
    expect((await screen.findByRole("menuitemradio", { name: "English" })).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("menuitemradio", { name: "fr" }).getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("menuitemradio", { name: "Track 3" }).getAttribute("aria-checked")).toBe("false");
  });

  it("picking a track enables it and disables the others", async () => {
    const m = el("video");
    const list = withTracks(m, [track({ label: "English", enabled: true }), track({ label: "French" })]);
    draw(m);
    await openMenu();
    await openSub("Audio track");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "French" }));
    expect(list[0].enabled).toBe(false);
    expect(list[1].enabled).toBe(true);
    await openMenu();
    await openSub("Audio track");
    expect((await screen.findByRole("menuitemradio", { name: "French" })).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("menuitemradio", { name: "English" }).getAttribute("aria-checked")).toBe("false");
  });

  it("re-reads the tracks on loadedmetadata", async () => {
    const m = el("video");
    withTracks(m, [track({ label: "Only", enabled: true })]);
    draw(m);
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Audio track" })).toBeNull();
    withTracks(m, [track({ label: "One", enabled: true }), track({ label: "Two" })]);
    act(() => {
      m.dispatchEvent(new Event("loadedmetadata"));
    });
    await openSub("Audio track");
    expect(await screen.findByRole("menuitemradio", { name: "Two" })).toBeTruthy();
  });

  it("keeps nothing between files: a new element reads its own tracks", async () => {
    const first = el("video");
    withTracks(first, [track({ label: "A", enabled: true }), track({ label: "B" })]);
    const one = draw(first);
    await openMenu();
    await openSub("Audio track");
    await screen.findByRole("menuitemradio", { name: "B" });
    one.unmount();

    const second = el("video");
    withTracks(second, [track({ label: "C" }), track({ label: "D", enabled: true })]);
    draw(second);
    await openMenu();
    await openSub("Audio track");
    expect((await screen.findByRole("menuitemradio", { name: "D" })).getAttribute("aria-checked")).toBe("true");
    expect(screen.queryByRole("menuitemradio", { name: "A" })).toBeNull();
    expect(Object.keys(window.localStorage).filter((k) => k.toLowerCase().includes("audio"))).toEqual([]);
  });
});

describe("the Save frame item", () => {
  // The canvas the capture draws on is stubbed, as lib/files/frame.test.ts stubs it: jsdom draws nothing. The blob
  // the stub gives is the one the save must receive.
  const PNG = new Blob(["png"], { type: "image/png" });
  const proto = HTMLCanvasElement.prototype;
  let kept: { getContext?: PropertyDescriptor; toBlob?: PropertyDescriptor } = {};

  function stubCanvas(opts: { draw?: () => void; blob?: Blob | null } = {}) {
    const drawImage = vi.fn(() => opts.draw?.());
    Object.defineProperty(proto, "getContext", {
      configurable: true,
      value(this: HTMLCanvasElement, kind: string) {
        return kind === "2d" ? ({ drawImage } as unknown as CanvasRenderingContext2D) : null;
      },
    });
    Object.defineProperty(proto, "toBlob", {
      configurable: true,
      value(_cb: BlobCallback, _type?: string) {
        _cb(opts.blob === undefined ? PNG : opts.blob);
      },
    });
  }

  beforeEach(() => {
    kept = {
      getContext: Object.getOwnPropertyDescriptor(proto, "getContext"),
      toBlob: Object.getOwnPropertyDescriptor(proto, "toBlob"),
    };
  });

  afterEach(() => {
    for (const k of ["getContext", "toBlob"] as const) {
      const d = kept[k];
      if (d) Object.defineProperty(proto, k, d);
      else delete (proto as unknown as Record<string, unknown>)[k];
    }
  });

  function videoEl(over: { ready?: number; at?: number } = {}): HTMLVideoElement {
    const m = el("video") as HTMLVideoElement;
    Object.defineProperty(m, "videoWidth", { value: 160, configurable: true });
    Object.defineProperty(m, "videoHeight", { value: 120, configurable: true });
    Object.defineProperty(m, "readyState", { value: over.ready ?? 4, configurable: true });
    at(m, over.at ?? 1.2);
    return m;
  }

  function drawFrames(m: HTMLMediaElement, fr?: { save: (blob: Blob, seconds: number) => void }) {
    return render(
      <App>
        <MediaExtras media={m} popupHost={host} frames={fr} />
      </App>,
    );
  }

  it("shows Save frame on a video only, and only when frames is given", async () => {
    const audio = drawFrames(el("audio"), { save: vi.fn() });
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Save frame" })).toBeNull();
    audio.unmount();

    const plain = drawFrames(videoEl());
    await openMenu();
    expect(screen.queryByRole("menuitem", { name: "Save frame" })).toBeNull();
    plain.unmount();

    drawFrames(videoEl(), { save: vi.fn() });
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Save frame" })).toBeTruthy();
  });

  it("a click captures the frame at the place on screen and saves the blob with that time", async () => {
    stubCanvas();
    const save = vi.fn();
    drawFrames(videoEl({ at: 1.2 }), { save });
    await openMenu();
    await pick("Save frame");
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save).toHaveBeenCalledWith(PNG, 1.2);
    expect(document.querySelector("canvas")).toBeNull();
  });

  it("a video with no picture yet says its own note and saves nothing", async () => {
    stubCanvas();
    const save = vi.fn();
    drawFrames(videoEl({ ready: 1 }), { save });
    await openMenu();
    await pick("Save frame");
    expect(await screen.findByText("The video has no picture to save yet. Let it play a moment and try again.")).toBeTruthy();
    expect(save).not.toHaveBeenCalled();
  });

  it("a canvas the browser will not read says its own note and saves nothing", async () => {
    stubCanvas({ draw: () => { throw new DOMException("The canvas is tainted.", "SecurityError"); } });
    const save = vi.fn();
    const tainted = drawFrames(videoEl(), { save });
    await openMenu();
    await pick("Save frame");
    expect(await screen.findByText("This video's picture cannot be read, so its frame cannot be saved.")).toBeTruthy();
    expect(save).not.toHaveBeenCalled();
    tainted.unmount();

    stubCanvas({ blob: null });
    const saveNull = vi.fn();
    drawFrames(videoEl(), { save: saveNull });
    await openMenu();
    await pick("Save frame");
    expect(await screen.findByText("This video's picture cannot be read, so its frame cannot be saved.")).toBeTruthy();
    expect(saveNull).not.toHaveBeenCalled();
  });
});

describe("the Equalizer panel", () => {
  beforeEach(() => {
    window.localStorage.clear();
    eqGraph.attach.mockClear();
    eqGraph.attach.mockImplementation(async () => true);
    eqGraph.setGains.mockClear();
  });

  afterEach(() => {
    window.localStorage.clear();
    session.set(null);
  });

  async function openEqualizer() {
    await openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Equalizer…" }));
    return screen.findByRole("slider", { name: "31 Hz" });
  }

  const kept = () => JSON.parse(window.localStorage.getItem("selfcloud.equalizer.visitor") ?? "{}") as { on: boolean; gains: number[] };

  it.each(["audio", "video"] as const)("offers Equalizer… on %s, with the switch, the presets and the ten sliders", async (kind) => {
    draw(el(kind));
    expect(await openEqualizer()).toBeTruthy();
    expect(screen.getByRole("switch", { name: "On" })).toBeTruthy();
    for (const name of ["Flat", "Bass", "Treble", "Vocal"]) expect(screen.getByRole("radio", { name })).toBeTruthy();
    for (const name of ["31 Hz", "62 Hz", "125 Hz", "250 Hz", "500 Hz", "1 kHz", "2 kHz", "4 kHz", "8 kHz", "16 kHz"]) {
      expect(screen.getByRole("slider", { name })).toBeTruthy();
    }
    expect(screen.getByRole("button", { name: "Reset" })).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Close" }).length).toBeGreaterThan(0);
  });

  it("a slider writes the kept choice and calls setGains", async () => {
    const m = el();
    draw(m);
    const handle = await openEqualizer();
    fireEvent.click(screen.getByRole("switch", { name: "On" }));
    await waitFor(() => expect(eqGraph.attach).toHaveBeenCalledWith(m));
    for (let i = 0; i < 3; i++) fireEvent.keyDown(handle, { keyCode: UP });
    expect(screen.getByText("+3 dB")).toBeTruthy();
    expect(kept().on).toBe(true);
    expect(kept().gains).toEqual([3, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    await waitFor(() => expect(eqGraph.setGains).toHaveBeenCalledWith(m, [3, 0, 0, 0, 0, 0, 0, 0, 0, 0]));
    // The gains match no preset: Custom shows, checked.
    expect((screen.getByRole("radio", { name: "Custom" }) as HTMLInputElement).checked).toBe(true);
  });

  it("a preset sets the ten gains and keeps them", async () => {
    const m = el();
    draw(m);
    await openEqualizer();
    fireEvent.click(screen.getByRole("switch", { name: "On" }));
    await waitFor(() => expect(eqGraph.attach).toHaveBeenCalledWith(m));
    fireEvent.click(screen.getByRole("radio", { name: "Bass" }));
    await waitFor(() => expect(eqGraph.setGains).toHaveBeenCalledWith(m, PRESETS.bass));
    expect(kept().gains).toEqual(PRESETS.bass);
    expect(screen.getByText("+6 dB")).toBeTruthy();
  });

  it("Reset returns the gains to flat and keeps them", async () => {
    draw(el());
    await openEqualizer();
    fireEvent.click(screen.getByRole("switch", { name: "On" }));
    await waitFor(() => expect(eqGraph.attach).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("radio", { name: "Treble" }));
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(kept().gains).toEqual(PRESETS.flat);
    expect((screen.getByRole("radio", { name: "Flat" }) as HTMLInputElement).checked).toBe(true);
  });

  it("says so in one line when attach returns false, and the switch stays usable", async () => {
    eqGraph.attach.mockImplementation(async () => false);
    draw(el());
    await openEqualizer();
    const sw = screen.getByRole("switch", { name: "On" });
    fireEvent.click(sw);
    expect(await screen.findByText("The equalizer cannot change this file's sound.")).toBeTruthy();
    expect(sw.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(sw);
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("false"));
    expect(kept().on).toBe(false);
  });

  it("a kept on choice applies to a new element at its first play, through attach alone", async () => {
    writeEq(null, { on: true, gains: [...PRESETS.bass] });
    const m = el();
    draw(m);
    expect(eqGraph.attach).not.toHaveBeenCalled();
    act(() => {
      m.dispatchEvent(new Event("play"));
    });
    await waitFor(() => expect(eqGraph.attach).toHaveBeenCalledWith(m));
    expect(eqGraph.setGains).toHaveBeenCalledWith(m, PRESETS.bass);
  });

  it("a kept on choice that cannot attach leaves the element plain", async () => {
    eqGraph.attach.mockImplementation(async () => false);
    writeEq(null, { on: true, gains: [...PRESETS.bass] });
    const m = el();
    draw(m);
    act(() => {
      m.dispatchEvent(new Event("play"));
    });
    await waitFor(() => expect(eqGraph.attach).toHaveBeenCalledWith(m));
    expect(eqGraph.setGains).not.toHaveBeenCalled();
  });
});
