// SPDX-License-Identifier: AGPL-3.0-or-later

// The media session in the shell: the viewer, the mini player that stays when the viewer closes on audio, the place the
// person stopped at, the subtitles, and the end of the session on a sign-out or a stop.

import { act, configure, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import { closeViewer, getPlayback, showViewer, startPlayback, stopPlayback } from "../../lib/files/playback";
import { Pager } from "../../lib/files/pager";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import MediaHost from "./MediaHost";

// The viewer and its player load lazily, so the first wait allows for that load.
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

function file(name: string, mime: string, size = 10): Node {
  return {
    id: `n-${name}`,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size,
    mime_type: mime,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
  } as unknown as Node;
}

const folderOf = (list: Node[]) => new Pager(list, null, async () => ({ items: [] }));

// at sets where the element is, as the browser reports it.
function at(el: HTMLMediaElement, seconds: number, paused = false) {
  Object.defineProperty(el, "currentTime", { value: seconds, writable: true, configurable: true });
  Object.defineProperty(el, "paused", { value: paused, configurable: true });
  // The browser has the file's data once it reports its metadata: a place is applied only then.
  Object.defineProperty(el, "readyState", { value: 1, configurable: true });
}

const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;

async function element(tag: "video" | "audio"): Promise<HTMLMediaElement> {
  return waitFor(() => {
    const el = document.querySelector(tag);
    if (!el) throw new Error(`no ${tag} yet`);
    return el as HTMLMediaElement;
  });
}

let play: ReturnType<typeof vi.spyOn>;
let pause: ReturnType<typeof vi.spyOn>;

// The viewer's module is loaded once here, so the first test's wait does not include a cold load of the dock and its player.
beforeAll(async () => {
  await import("./MediaDock");
}, 60000);

beforeEach(() => {
  signedLink.mockResolvedValue({ url: "https://content.example/a", disposition: "inline", expires_at: "" });
  getResume.mockResolvedValue({ position_seconds: 0, duration_seconds: 0 });
  putResume.mockResolvedValue(undefined);
  listSubtitles.mockResolvedValue([]);
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  stopPlayback();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  signedLink.mockReset();
  getResume.mockReset();
  putResume.mockReset();
  listSubtitles.mockReset();
  startDownload.mockReset();
});

describe("the first file opened starts on load", () => {
  it("plays the file the viewer opened, without a second file or a press", async () => {
    const a = file("a.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    const video = await element("video");
    fireEvent.loadedMetadata(video);
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("shows the press-Play note when the browser refuses the start for want of a press", async () => {
    play.mockImplementation(() => Promise.reject(new DOMException("needs a gesture", "NotAllowedError")));
    const a = file("a.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    fireEvent.loadedMetadata(await element("video"));
    expect(await screen.findByTestId("media-press")).toBeTruthy();
  });

  it("shows no note for a refusal that is not for want of a press", async () => {
    play.mockImplementation(() => Promise.reject(new Error("decoder")));
    const a = file("a.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    fireEvent.loadedMetadata(await element("video"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.queryByTestId("media-press")).toBeNull();
  });
});

describe("the playlist's current row says what the player does", () => {
  it("says Paused before the file has started, Playing while it plays, and Paused again after a pause", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    const audio = await element("audio");
    await waitFor(() => expect(document.querySelector(".media-state")?.textContent).toContain("Paused"));
    at(audio, 0, false);
    fireEvent.play(audio);
    await waitFor(() => expect(document.querySelector(".media-state")?.textContent).toContain("Playing"));
    at(audio, 1, true);
    fireEvent.pause(audio);
    await waitFor(() => expect(document.querySelector(".media-state")?.textContent).toContain("Paused"));
    expect(document.querySelector(".media-state")?.textContent).not.toContain("Playing");
  });
});

describe("audio goes on after the viewer closes", () => {
  it("keeps the same element playing, and the mini player shows it", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    const audio = await element("audio");
    fireEvent.loadedMetadata(audio);
    act(() => closeViewer());
    expect(getPlayback().viewerOpen).toBe(false);
    expect(await screen.findByTestId("media-mini")).toBeTruthy();
    expect(document.querySelector("audio")).toBe(audio);
    expect(pause).not.toHaveBeenCalled();
    act(() => showViewer());
    await waitFor(() => expect(getPlayback().viewerOpen).toBe(true));
  });

  it("drops the session when a video's viewer closes: nothing plays on", async () => {
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    fireEvent.loadedMetadata(video);
    act(() => closeViewer());
    expect(getPlayback().seed).toBeNull();
    await waitFor(() => expect(document.querySelector("video")).toBeNull());
    expect(pause).toHaveBeenCalled();
  });

  it("plays and pauses from the mini player, through the same element", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    const audio = await element("audio");
    fireEvent.loadedMetadata(audio);
    act(() => closeViewer());
    const mini = await screen.findByTestId("media-mini");
    const playButton = mini.querySelector(".media-mini-play") as HTMLButtonElement;
    fireEvent.click(playButton);
    expect(play).toHaveBeenCalled();
  });

  it("stops at once from the mini player's close: the audio stops and the queue goes", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    fireEvent.loadedMetadata(await element("audio"));
    act(() => closeViewer());
    fireEvent.click(await screen.findByRole("button", { name: "Stop and close" }));
    expect(getPlayback().seed).toBeNull();
    await waitFor(() => expect(screen.queryByTestId("media-mini")).toBeNull());
    expect(pause).toHaveBeenCalled();
  });
});

describe("a hidden viewer is not an open dialog", () => {
  it("bears no dialog role, aria-modal or test id once closed, and the audio element goes on", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    const audio = await element("audio");
    expect(document.querySelector('[data-testid="preview"]')).not.toBeNull();
    fireEvent.loadedMetadata(audio);
    act(() => closeViewer());
    await screen.findByTestId("media-mini");
    expect(document.querySelector('[data-testid="preview"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.querySelector("[aria-modal]")).toBeNull();
    expect(document.querySelector("audio")).toBe(audio);
  });

  it("an open viewer is a modal dialog, and Escape closes it", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    await element("audio");
    const dialog = await screen.findByRole("dialog");
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.getAttribute("data-testid")).toBe("preview");
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(getPlayback().viewerOpen).toBe(false));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Tab from the last control goes back to the first, so focus stays in the open viewer", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    await element("audio");
    const dialog = await screen.findByRole("dialog");
    const controls = Array.from(dialog.querySelectorAll<HTMLElement>("button, [tabindex]:not([tabindex='-1'])")).filter((el) => !el.hasAttribute("disabled"));
    const first = controls[0];
    const last = controls[controls.length - 1];
    last.focus();
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);
  });
});

describe("a change of person ends the session", () => {
  it("stops the audio and drops the queue when the person changes", async () => {
    const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;
    session.set(who("usr_a"));
    const a = file("a.ogg", "audio/ogg");
    startPlayback({ start: a, folder: folderOf([a]) });
    expect(getPlayback().seed).not.toBeNull();
    session.set(who("usr_b"));
    expect(getPlayback().seed).toBeNull();
    expect(getPlayback().current).toBeNull();
    session.set(null);
  });
});

describe("the place a person stopped at", () => {
  it("starts a file at the place saved for the person, and saves it on a tick and on pause", async () => {
    getResume.mockResolvedValue({ position_seconds: 30, duration_seconds: 100 });
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    at(video, 0, false);
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(30);
    expect(getResume).toHaveBeenCalledWith("n-clip.mp4");
    fireEvent.timeUpdate(video);
    expect(putResume).toHaveBeenCalledTimes(1);
    expect(putResume).toHaveBeenLastCalledWith("n-clip.mp4", 30, 0);
    // A second tick inside fifteen seconds writes nothing more.
    fireEvent.timeUpdate(video);
    expect(putResume).toHaveBeenCalledTimes(1);
    at(video, 42, true);
    fireEvent.pause(video);
    expect(putResume).toHaveBeenLastCalledWith("n-clip.mp4", 42, 0);
  });

  it("starts from the beginning when the saved place is near the end", async () => {
    getResume.mockResolvedValue({ position_seconds: 95, duration_seconds: 100 });
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    at(video, 0, false);
    Object.defineProperty(video, "duration", { value: 100, configurable: true });
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(0);
  });

  it("keeps a file that ends at the beginning", async () => {
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    Object.defineProperty(video, "duration", { value: 60, configurable: true });
    Object.defineProperty(video, "ended", { value: true, configurable: true });
    fireEvent.ended(video);
    expect(putResume).toHaveBeenLastCalledWith("n-clip.mp4", 0, 60);
  });
});

describe("the place is kept under the person it belongs to", () => {
  it("writes nothing when the person changes while the file's player is still open", async () => {
    session.set(who("usr_a"));
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    at(video, 20, false);
    fireEvent.loadedMetadata(video);
    // The place is known (none stored), so a save is due when the player goes.
    await waitFor(() => expect(getResume).toHaveBeenCalled());
    await act(async () => {
      await Promise.resolve();
    });
    act(() => session.set(who("usr_b")));
    await waitFor(() => expect(document.querySelector("video")).toBeNull());
    expect(putResume).not.toHaveBeenCalled();
    session.set(null);
  });
});

describe("a file starts without waiting for its saved place or its subtitles", () => {
  it("shows the player while the place is still being read", async () => {
    getResume.mockReturnValue(new Promise(() => {}));
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    expect(await element("video")).toBeTruthy();
  });

  it("does not ask for the subtitles of an audio file", async () => {
    const a = file("a.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    await element("audio");
    await act(async () => {
      await Promise.resolve();
    });
    expect(listSubtitles).not.toHaveBeenCalled();
  });
});

describe("a refused start from the mini player", () => {
  it("shows the press-Play note in the mini bar, where the viewer is not", async () => {
    const a = file("a.ogg", "audio/ogg");
    const b = file("b.ogg", "audio/ogg");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a, b]) }));
    fireEvent.loadedMetadata(await element("audio"));
    act(() => closeViewer());
    play.mockImplementation(() => Promise.reject(new DOMException("needs a gesture", "NotAllowedError")));
    const mini = await screen.findByTestId("media-mini");
    fireEvent.click(within(mini).getByRole("button", { name: "Next file" }));
    await waitFor(() => expect(signedLink).toHaveBeenCalledTimes(2));
    fireEvent.loadedMetadata(await element("audio"));
    expect(await within(await screen.findByTestId("media-mini")).findByTestId("media-press")).toBeTruthy();
  });
});

describe("a place that arrives late", () => {
  it("is applied when the file has started but the person has not moved the playhead", async () => {
    let answer!: (v: { position_seconds: number; duration_seconds: number }) => void;
    getResume.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    at(video, 0, false);
    Object.defineProperty(video, "duration", { value: 100, configurable: true });
    fireEvent.loadedMetadata(video);
    await act(async () => {
      answer({ position_seconds: 42, duration_seconds: 100 });
    });
    await waitFor(() => expect(video.currentTime).toBe(42));
  });
});

describe("subtitles beside a video", () => {
  it("offers each subtitle file as a track, with none shown until one is picked", async () => {
    const tracks: { mode: string; cues: Array<[number, number, string]> }[] = [];
    Object.defineProperty(HTMLMediaElement.prototype, "addTextTrack", {
      configurable: true,
      value: function addTextTrack() {
        const t = { mode: "hidden", cues: [] as Array<[number, number, string]>, addCue(c: { startTime: number; endTime: number; text: string }) { t.cues.push([c.startTime, c.endTime, c.text]); } };
        tracks.push(t);
        return t;
      },
    });
    vi.stubGlobal("VTTCue", class { constructor(public startTime: number, public endTime: number, public text: string) {} });
    listSubtitles.mockResolvedValue([{ id: "s-1", name: "clip.vtt", format: "vtt" }]);
    vi.stubGlobal("fetch", vi.fn(async () => new Response("WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello there", { status: 200 })));
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    const video = await element("video");
    fireEvent.loadedMetadata(video);
    await waitFor(() => expect(tracks).toHaveLength(1));
    expect(tracks[0].cues).toEqual([[1, 2.5, "Hello there"]]);
    expect(tracks[0].mode).toBe("disabled");
    const sub = await screen.findByRole("combobox", { name: "Subtitles" });
    fireEvent.mouseDown(sub.closest(".ant-select")?.querySelector(".ant-select-selector") as Element);
    fireEvent.click(await screen.findByText("clip.vtt"));
    await waitFor(() => expect(tracks[0].mode).toBe("showing"));
    delete (HTMLMediaElement.prototype as { addTextTrack?: unknown }).addTextTrack;
  });

  it("gives no track for a subtitle file the server refuses", async () => {
    const add = vi.fn();
    Object.defineProperty(HTMLMediaElement.prototype, "addTextTrack", { configurable: true, value: add });
    vi.stubGlobal("VTTCue", class {});
    listSubtitles.mockResolvedValue([{ id: "s-1", name: "clip.srt", format: "srt" }]);
    vi.stubGlobal("fetch", vi.fn(async () => new Response("no", { status: 403 })));
    const v = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: v, folder: folderOf([v]) }));
    fireEvent.loadedMetadata(await element("video"));
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(add).not.toHaveBeenCalled();
    expect(screen.queryByRole("combobox", { name: "Subtitles" })).toBeNull();
    delete (HTMLMediaElement.prototype as { addTextTrack?: unknown }).addTextTrack;
  });
});

describe("the viewer's own behaviour", () => {
  it("does not put the focus on Close when it opens, so a Space press reaches the player", async () => {
    const a = file("a.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    await element("video");
    await waitFor(() => expect(document.querySelector("video")).not.toBeNull());
    expect(document.activeElement).not.toBe(screen.getByRole("button", { name: "Close" }));
  });

  it("shows the downloads-off notice when the server refuses the download", async () => {
    startDownload.mockResolvedValue(false);
    const a = file("a.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a]) }));
    await element("video");
    fireEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(startDownload).toHaveBeenCalledWith("n-a.mp4");
    expect(await screen.findByText("Downloads are off for this file.")).toBeTruthy();
  });

  it("says a video this browser cannot play is unsupported, after one new link that fails the same way", async () => {
    const hevc = file("hevc.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: hevc, folder: folderOf([hevc]) }));
    const video = await element("video");
    Object.defineProperty(video, "error", { value: { code: 4 }, configurable: true });
    fireEvent.error(video);
    await waitFor(() => expect(signedLink).toHaveBeenCalledTimes(2));
    fireEvent.error(video);
    expect(await screen.findByText("This browser can't play this format.")).toBeTruthy();
    expect(signedLink).toHaveBeenCalledTimes(2);
  });

  it("pauses a video when the playlist moves to the next file", async () => {
    const a = file("a.mp4", "video/mp4");
    const b = file("b.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: a, folder: folderOf([a, b]) }));
    await element("video");
    pause.mockClear();
    fireEvent.click(await screen.findByRole("button", { name: "Next file" }));
    await waitFor(() => expect(signedLink).toHaveBeenCalledTimes(2));
    expect(pause).toHaveBeenCalled();
  });

  it("shows the restricted card when a renewed link is refused as a download restriction", async () => {
    signedLink
      .mockResolvedValueOnce({ url: "https://content.example/first", disposition: "inline", expires_at: "" })
      .mockRejectedValueOnce(new ApiError(403, "download_restricted", {}, 0));
    const clip = file("clip.mp4", "video/mp4");
    render(<MediaHost />);
    act(() => startPlayback({ start: clip, folder: folderOf([clip]) }));
    const video = await element("video");
    Object.defineProperty(video, "currentTime", { value: 0, configurable: true });
    Object.defineProperty(video, "error", { value: { code: 4 }, configurable: true });
    fireEvent.error(video);
    expect(await screen.findByText("Downloads are off for this file.")).toBeTruthy();
    expect(screen.queryByText("Something went wrong. Try again.")).toBeNull();
  });
});
