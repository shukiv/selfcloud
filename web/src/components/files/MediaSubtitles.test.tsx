// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's Subtitles menu: one button for every video, the tracks beside the file and a file from this device, the
// timing offset kept per video file, and the size kept per person. The element's text tracks are stubbed as the browser
// holds them (real cue objects), so an offset's shift of every cue can be read.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_SUBTITLE_BYTES, type SubtitleTrack } from "../../lib/files/subtitles";
import { session, type Session } from "../../lib/session";
import MediaPlayer from "./MediaPlayer";

const LINK = { url: "https://content.example/clip.webm" };
const mint = async () => LINK;

class StubCue {
  snapToLines = true;
  line: number | string = 0;
  lineAlign = "start";
  constructor(public startTime: number, public endTime: number, public text: string) {}
}
interface StubTrack {
  kind: string;
  label: string;
  mode: string;
  cues: StubCue[];
  addCue(c: StubCue): void;
}

let made: StubTrack[];

const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;

const TRACKS: SubtitleTrack[] = [
  { label: "clip.vtt", cues: [{ start: 1, end: 25, text: "Hello there" }] },
  { label: "clip.srt", cues: [{ start: 2, end: 26, text: "שלום" }] },
];

const SRT = "1\n00:00:01,000 --> 00:00:02,000\nHi there\n";

function video(): HTMLVideoElement {
  return document.querySelector("video") as HTMLVideoElement;
}
function subsButton(): HTMLElement {
  return screen.getByRole("button", { name: "Subtitles" });
}
// openMenu opens the Subtitles menu and returns it. The popup is destroyed when it closes, so one menu is on the page.
async function openMenu(): Promise<HTMLElement> {
  fireEvent.click(subsButton());
  return screen.findByRole("menu");
}
function itemOf(menu: HTMLElement, name: string): HTMLElement {
  return within(menu).getByText(name).closest("li") as HTMLElement;
}
// timingClick opens the menu, opens its Timing submenu, and clicks the item named.
async function timingClick(name: string): Promise<void> {
  const menu = await openMenu();
  fireEvent.click(within(menu).getByText("Timing").closest(".ant-dropdown-menu-submenu-title") as Element);
  fireEvent.click((await screen.findByText(name)).closest("li") as Element);
}
// sizeClick opens the menu, opens its Size submenu, and clicks the item named.
async function sizeClick(name: string): Promise<void> {
  const menu = await openMenu();
  fireEvent.click(within(menu).getByText("Size").closest(".ant-dropdown-menu-submenu-title") as Element);
  fireEvent.click((await screen.findByText(name)).closest("li") as Element);
}
function fileInput(): HTMLInputElement {
  return screen.getByTestId("media-subs-file") as HTMLInputElement;
}
function loadFile(file: File): void {
  fireEvent.change(fileInput(), { target: { files: [file] } });
}
// renderPlayer draws the player with antd's motion off. jsdom never fires transitionend, so a popup's leave motion would
// never finish and destroyOnHidden would never run; with motion off the popup is destroyed on close, as in a browser.
function renderPlayer(ui: ReactElement) {
  return render(<ConfigProvider theme={{ token: { motion: false } }}>{ui}</ConfigProvider>);
}

beforeEach(() => {
  made = [];
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  Object.defineProperty(HTMLMediaElement.prototype, "addTextTrack", {
    configurable: true,
    value(kind: string, label: string) {
      const t: StubTrack = { kind, label, mode: "hidden", cues: [], addCue(c: StubCue) { t.cues.push(c); } };
      made.push(t);
      return t;
    },
  });
  vi.stubGlobal("VTTCue", StubCue);
  localStorage.clear();
  session.set(null);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (HTMLMediaElement.prototype as { addTextTrack?: unknown }).addTextTrack;
  Object.defineProperty(document, "fullscreenElement", { configurable: true, value: null });
  localStorage.clear();
  session.set(null);
});

describe("the Subtitles button", () => {
  it("shows for a video with no subtitle files beside it, and never for audio", () => {
    const { unmount } = renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    expect(subsButton().getAttribute("aria-haspopup")).toBe("menu");
    unmount();
    renderPlayer(<MediaPlayer kind="audio" name="song.mp3" link={LINK} mint={mint} onFail={() => {}} />);
    expect(screen.queryByRole("button", { name: "Subtitles" })).toBeNull();
  });

  it("opens its popup on the stage in full screen, so the menu shows there", async () => {
    const { container } = renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const stage = container.querySelector('[data-testid="media-stage"]') as HTMLElement;
    Object.defineProperty(document, "fullscreenElement", { configurable: true, value: stage });
    const menu = await openMenu();
    expect(stage.contains(menu.closest(".ant-dropdown"))).toBe(true);
  });
});

describe("the tracks in the menu", () => {
  it("lists Off and each track, and picking one shows it and disables the others", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} />);
    expect(made.map((t) => t.mode)).toEqual(["disabled", "disabled"]);
    const menu = await openMenu();
    const off = itemOf(menu, "Off");
    const a = itemOf(menu, "clip.vtt");
    const b = itemOf(menu, "clip.srt");
    expect(off.getAttribute("role")).toBe("menuitemradio");
    expect(a.getAttribute("role")).toBe("menuitemradio");
    expect(off.getAttribute("aria-checked")).toBe("true");
    expect(a.getAttribute("aria-checked")).toBe("false");
    expect(b.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(a);
    await waitFor(() => expect(made[0].mode).toBe("showing"));
    expect(made[1].mode).toBe("disabled");
    const again = await openMenu();
    expect(itemOf(again, "clip.vtt").getAttribute("aria-checked")).toBe("true");
    expect(itemOf(again, "Off").getAttribute("aria-checked")).toBe("false");
    fireEvent.click(itemOf(again, "Off"));
    await waitFor(() => expect(made[0].mode).toBe("disabled"));
  });
});

describe("a file from this device", () => {
  it("adds a track named after the file, shows it at once, and makes no request", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} />);
    loadFile(new File([SRT], "local.srt", { type: "application/x-subrip" }));
    await waitFor(() => expect(made).toHaveLength(3));
    expect(made[2].label).toBe("local.srt");
    expect(made[2].mode).toBe("showing");
    expect(made[0].mode).toBe("disabled");
    expect(made[2].cues[0].text).toBe("Hi there");
    expect(fetchSpy).not.toHaveBeenCalled();
    // Beside files first, then the device's file, and the device's file is the checked one.
    const menu = await openMenu();
    const radios = within(menu).getAllByRole("menuitemradio");
    expect(radios.map((r) => r.textContent?.trim())).toEqual(["Off", "clip.vtt", "clip.srt", "local.srt"]);
    expect(radios[3].getAttribute("aria-checked")).toBe("true");
  });

  it("shows the note and adds nothing for a file with no cues", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    loadFile(new File(["not a subtitle file"], "empty.srt"));
    expect(await screen.findByText("No subtitles were found in that file")).toBeTruthy();
    expect(made).toHaveLength(0);
    // A file with cues after it clears the note and adds its track.
    loadFile(new File([SRT], "local.srt"));
    await waitFor(() => expect(made).toHaveLength(1));
    expect(screen.queryByText("No subtitles were found in that file")).toBeNull();
  });

  it("reads a file over the limit only up to the limit", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const big = new File([SRT + "\n" + "x".repeat(MAX_SUBTITLE_BYTES)], "big.srt");
    expect(big.size).toBeGreaterThan(MAX_SUBTITLE_BYTES);
    const slice = vi.spyOn(big, "slice");
    loadFile(big);
    await waitFor(() => expect(made).toHaveLength(1));
    expect(slice).toHaveBeenCalledWith(0, MAX_SUBTITLE_BYTES);
    expect(made[0].cues[0].text).toBe("Hi there");
  });
});

describe("the timing offset", () => {
  it("Later 0.1 s twice moves every cue of every track by +0.2 and writes it under the node's key", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    await timingClick("Later 0.1 s");
    await timingClick("Later 0.1 s");
    expect(made[0].cues[0].startTime).toBeCloseTo(1.2, 5);
    expect(made[0].cues[0].endTime).toBeCloseTo(25.2, 5);
    expect(made[1].cues[0].startTime).toBeCloseTo(2.2, 5);
    expect(made[1].cues[0].endTime).toBeCloseTo(26.2, 5);
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBe("0.2");
  });

  it("applies the saved offset when the same node opens again, and not to another node", async () => {
    localStorage.setItem("selfcloud.subtitleOffset.node-1", "0.2");
    const first = renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    expect(made[0].cues[0].startTime).toBeCloseTo(1.2, 5);
    expect(made[0].cues[0].endTime).toBeCloseTo(25.2, 5);
    first.unmount();
    made = [];
    renderPlayer(<MediaPlayer kind="video" name="other.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-2" />);
    expect(made[0].cues[0].startTime).toBe(1);
    expect(made[0].cues[0].endTime).toBe(25);
  });

  it("clamps at +60 and -60, and a cue never starts below 0", async () => {
    localStorage.setItem("selfcloud.subtitleOffset.node-1", "59.9");
    const first = renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    await timingClick("Later 0.1 s");
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBe("60");
    await timingClick("Later 0.1 s");
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBe("60");
    first.unmount();
    made = [];
    localStorage.setItem("selfcloud.subtitleOffset.node-1", "-60");
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    expect(made[0].cues[0].startTime).toBe(0);
    expect(made[0].cues[0].endTime).toBe(0);
    await timingClick("Earlier 0.1 s");
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBe("-60");
  });

  it("Reset writes 0 and puts every cue back at its own time", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    await timingClick("Later 0.1 s");
    await timingClick("Reset");
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBe("0");
    expect(made[0].cues[0].startTime).toBe(1);
    expect(made[0].cues[0].endTime).toBe(25);
  });

  it("shows the offset in the Timing submenu's first item, disabled", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    await timingClick("Later 0.1 s");
    const menu = await openMenu();
    fireEvent.click(within(menu).getByText("Timing").closest(".ant-dropdown-menu-submenu-title") as Element);
    const shown = (await screen.findByText("Offset +0.1 s")).closest("li") as HTMLElement;
    expect(shown.getAttribute("aria-disabled")).toBe("true");
  });

  it("without a node key the offset lasts for this viewing only", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} />);
    await timingClick("Later 0.1 s");
    expect(made[0].cues[0].startTime).toBeCloseTo(1.1, 5);
    expect(localStorage.getItem("selfcloud.subtitleOffset.node-1")).toBeNull();
  });
});

describe("the size", () => {
  it("Large sets media-cue-l and saves under the person's key; a second person keeps their own", async () => {
    session.set(who("a"));
    const first = renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    expect(video().className).toContain("media-cue-m");
    await sizeClick("Large");
    expect(video().className).toContain("media-cue-l");
    expect(localStorage.getItem("selfcloud.subtitleSize.a")).toBe("l");
    first.unmount();
    session.set(who("b"));
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    expect(video().className).toContain("media-cue-m");
    expect(video().className).not.toContain("media-cue-l");
    await sizeClick("Small");
    expect(video().className).toContain("media-cue-s");
    expect(localStorage.getItem("selfcloud.subtitleSize.b")).toBe("s");
    expect(localStorage.getItem("selfcloud.subtitleSize.a")).toBe("l");
  });

  it("a visitor's size is kept under the visitor key", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    await sizeClick("Large");
    expect(localStorage.getItem("selfcloud.subtitleSize.visitor")).toBe("l");
  });

  it("the chosen size is the checked one in the Size submenu", async () => {
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const menu = await openMenu();
    fireEvent.click(within(menu).getByText("Size").closest(".ant-dropdown-menu-submenu-title") as Element);
    const medium = (await screen.findByText("Medium")).closest("li") as HTMLElement;
    expect(medium.getAttribute("role")).toBe("menuitemradio");
    expect(medium.getAttribute("aria-checked")).toBe("true");
    expect((screen.getByText("Large").closest("li") as HTMLElement).getAttribute("aria-checked")).toBe("false");
  });
});

describe("a storage that throws", () => {
  it("still applies the size and the offset for this page", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    renderPlayer(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} tracks={TRACKS} subtitleKey="node-1" />);
    await sizeClick("Large");
    expect(video().className).toContain("media-cue-l");
    await timingClick("Later 0.1 s");
    expect(made[0].cues[0].startTime).toBeCloseTo(1.1, 5);
  });
});
