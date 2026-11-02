// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bindMediaSession, setPlaybackState, SEEK_S } from "./mediaSession";

type Handler = MediaSessionActionHandler | null;

// fakeSession records what the page sets on the browser's Media Session.
function fakeSession(refuse: string[] = []) {
  const handlers = new Map<string, Handler>();
  const ms = {
    metadata: null as unknown,
    playbackState: "none" as string,
    position: null as unknown,
    setActionHandler(action: string, fn: Handler) {
      if (refuse.includes(action)) throw new TypeError("not supported");
      handlers.set(action, fn);
    },
    setPositionState(p: unknown) {
      ms.position = p;
    },
  };
  return { ms, handlers };
}

let fake: ReturnType<typeof fakeSession>;

beforeEach(() => {
  fake = fakeSession();
  Object.defineProperty(navigator, "mediaSession", { value: fake.ms, configurable: true });
  vi.stubGlobal("MediaMetadata", class {
    constructor(public init: { title: string }) {}
  });
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "mediaSession");
  vi.unstubAllGlobals();
});

describe("bindMediaSession: the keys and the lock screen act on the file that plays", () => {
  it("shows the artist, the album and the cover when they are known, and none when they are not", () => {
    const h = { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() };
    bindMediaSession({ title: "Song", artist: "A", album: "B", artwork: "/cover" }, h);
    expect((fake.ms.metadata as { init: unknown }).init).toEqual({
      title: "Song",
      artist: "A",
      album: "B",
      artwork: [{ src: "/cover", sizes: "256x256", type: "image/jpeg" }],
    });
    bindMediaSession({ title: "track.mp3" }, h);
    expect((fake.ms.metadata as { init: unknown }).init).toEqual({ title: "track.mp3", artist: "", album: "", artwork: [] });
  });

  it("shows the title and routes each action to its handler", () => {
    const h = { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() };
    bindMediaSession("song.ogg", h);
    expect((fake.ms.metadata as { init: { title: string } }).init.title).toBe("song.ogg");
    fake.handlers.get("play")?.({ action: "play" } as MediaSessionActionDetails);
    fake.handlers.get("pause")?.({ action: "pause" } as MediaSessionActionDetails);
    fake.handlers.get("nexttrack")?.({ action: "nexttrack" } as MediaSessionActionDetails);
    fake.handlers.get("previoustrack")?.({ action: "previoustrack" } as MediaSessionActionDetails);
    fake.handlers.get("seekforward")?.({ action: "seekforward" } as MediaSessionActionDetails);
    fake.handlers.get("seekbackward")?.({ action: "seekbackward" } as MediaSessionActionDetails);
    expect(h.play).toHaveBeenCalledTimes(1);
    expect(h.pause).toHaveBeenCalledTimes(1);
    expect(h.next).toHaveBeenCalledTimes(1);
    expect(h.previous).toHaveBeenCalledTimes(1);
    expect(h.seekBy).toHaveBeenCalledWith(SEEK_S);
    expect(h.seekBy).toHaveBeenCalledWith(-SEEK_S);
  });

  it("passes a seek to its time only when the time is a finite number", () => {
    const h = { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() };
    bindMediaSession("x", h);
    fake.handlers.get("seekto")?.({ action: "seekto", seekTime: 12 } as MediaSessionActionDetails);
    fake.handlers.get("seekto")?.({ action: "seekto", seekTime: Number.NaN } as MediaSessionActionDetails);
    expect(h.seekTo).toHaveBeenCalledTimes(1);
    expect(h.seekTo).toHaveBeenCalledWith(12);
  });

  it("clears every action, and the title, when unbound: a control never reaches a file that is gone", () => {
    const unbind = bindMediaSession("x", { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() });
    // The state must be something other than "none" before the unbind, or the reset cannot be seen.
    setPlaybackState(true, 5, 100);
    expect(fake.ms.playbackState).toBe("playing");
    unbind();
    for (const action of ["play", "pause", "nexttrack", "previoustrack", "seekforward", "seekbackward", "seekto"]) {
      expect(fake.handlers.get(action)).toBeNull();
    }
    expect(fake.ms.metadata).toBeNull();
    expect(fake.ms.playbackState).toBe("none");
  });

  it("sets the other actions when the browser refuses one", () => {
    fake = fakeSession(["nexttrack"]);
    Object.defineProperty(navigator, "mediaSession", { value: fake.ms, configurable: true });
    const h = { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() };
    bindMediaSession("x", h);
    fake.handlers.get("play")?.({ action: "play" } as MediaSessionActionDetails);
    expect(h.play).toHaveBeenCalledTimes(1);
    expect(fake.handlers.has("nexttrack")).toBe(false);
  });

  it("does nothing in a browser with no Media Session", () => {
    Reflect.deleteProperty(navigator, "mediaSession");
    const unbind = bindMediaSession("x", { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), seekBy: vi.fn(), seekTo: vi.fn() });
    expect(() => unbind()).not.toThrow();
  });
});

describe("setPlaybackState tells the lock screen whether the file plays", () => {
  it("says playing or paused, and the position when it is inside the length", () => {
    setPlaybackState(true, 5, 100);
    expect(fake.ms.playbackState).toBe("playing");
    expect(fake.ms.position).toEqual({ duration: 100, position: 5, playbackRate: 1 });
    setPlaybackState(false, 5, 0);
    expect(fake.ms.playbackState).toBe("paused");
    expect(fake.ms.position).toEqual({ duration: 100, position: 5, playbackRate: 1 });
  });
});
