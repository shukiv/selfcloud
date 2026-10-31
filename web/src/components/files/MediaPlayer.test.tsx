// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_RENEWALS } from "../../lib/files/media";
import MediaPlayer, { type Link, type MediaFailure } from "./MediaPlayer";

// fail gives the element the MediaError the browser would report, and fires the error event.
function fail(el: HTMLMediaElement, code: number) {
  Object.defineProperty(el, "error", { value: { code }, configurable: true });
  fireEvent.error(el);
}

// at sets where playback is, as the browser reports it.
function at(el: HTMLMediaElement, seconds: number, paused = false) {
  Object.defineProperty(el, "currentTime", { value: seconds, writable: true, configurable: true });
  Object.defineProperty(el, "paused", { value: paused, configurable: true });
}

const FRESH = "https://content.example/fresh";
const ORIGINAL = "https://content.example/original";

describe("MediaPlayer", () => {
  let pause: ReturnType<typeof vi.spyOn>;
  let play: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("asks for a new link when the first one has ended, and resumes at the same place", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 42);
    fail(el, 4);
    await waitFor(() => expect(el.getAttribute("src")).toBe(FRESH));
    expect(mint).toHaveBeenCalledTimes(1);
    expect(onFail).not.toHaveBeenCalled();
    // The new file loads: the player goes back to where it was, and plays on, as it was playing.
    fireEvent.loadedMetadata(el);
    expect(el.currentTime).toBe(42);
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("keeps a paused player paused after a renewal", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const { container } = render(<MediaPlayer kind="audio" name="song.mp3" link={{ url: ORIGINAL }} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("audio") as HTMLAudioElement;
    at(el, 7, true);
    fail(el, 2);
    await waitFor(() => expect(mint).toHaveBeenCalled());
    fireEvent.loadedMetadata(el);
    expect(play).not.toHaveBeenCalled();
  });

  // A file the browser cannot read fails at once, with no progress. One renewal is asked for, since a link can fail the
  // same way when it has ended; if the fresh link fails the same way, the format is the cause, and no more links are asked.
  it("asks once for a link that fails before playback moves, then says the format cannot be played", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn<(f: MediaFailure) => void>();
    const { container } = render(<MediaPlayer kind="video" name="broken.mp4" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0);
    fail(el, 4);
    await waitFor(() => expect(el.getAttribute("src")).toBe(FRESH));
    fireEvent.loadedMetadata(el);
    fail(el, 4);
    expect(onFail).toHaveBeenCalledWith({ kind: "unsupported" });
    expect(mint).toHaveBeenCalledTimes(1);
  });

  // The decision reads no clock. The server's own expiry may be ahead of or behind the browser's clock, so the same
  // file gets the same answer whatever the clock says.
  it.each([
    ["ahead by five minutes", 5 * 60_000],
    ["ahead by an hour", 60 * 60_000],
    ["behind by an hour", -60 * 60_000],
  ])("gives the same answer when the clock is %s", async (_label, skew) => {
    const realNow = Date.now();
    vi.spyOn(Date, "now").mockReturnValue(realNow + skew);
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn<(f: MediaFailure) => void>();
    const { container } = render(<MediaPlayer kind="video" name="broken.mp4" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0);
    fail(el, 3);
    await waitFor(() => expect(mint).toHaveBeenCalledTimes(1));
    fireEvent.loadedMetadata(el);
    fail(el, 3);
    expect(onFail).toHaveBeenCalledWith({ kind: "unsupported" });
    expect(mint).toHaveBeenCalledTimes(1);
  });

  // A real expired link, with the browser's clock far behind the server's: the failure before playback still gets a
  // renewal, and the player plays the file from the fresh link.
  it("renews a link that really expired when the clock is behind, and plays on", async () => {
    vi.spyOn(Date, "now").mockReturnValue(Date.now() - 60 * 60_000);
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="audio" name="song.mp3" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("audio") as HTMLAudioElement;
    at(el, 0);
    fail(el, 2);
    await waitFor(() => expect(el.getAttribute("src")).toBe(FRESH));
    expect(onFail).not.toHaveBeenCalled();
  });

  // The server may end a link sooner than five minutes. Nothing in the decision depends on how long the link was meant
  // to last, so a link that ends after playback has begun is renewed the same way.
  it("renews a link the server ended early, after playback had moved on", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 30);
    fail(el, 2);
    await waitFor(() => expect(el.getAttribute("src")).toBe(FRESH));
    expect(onFail).not.toHaveBeenCalled();
  });

  // A long video whose links keep ending asks for one link per stretch of playback, and keeps going.
  it("renews as often as a long video ends its links, when playback moves on each time", async () => {
    let n = 0;
    const mint = vi.fn(async (): Promise<Link> => ({ url: `https://content.example/${++n}` }));
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    for (let i = 0; i < MAX_RENEWALS + 2; i++) {
      at(el, 10 * (i + 1));
      fail(el, 4);
      await waitFor(() => expect(mint).toHaveBeenCalledTimes(i + 1));
      fireEvent.loadedMetadata(el);
    }
    expect(onFail).not.toHaveBeenCalled();
  });

  // The server signs a link from the node, the account, the disposition and its expiry in whole seconds, with no nonce: a
  // link asked for in the same second as the one that failed has the same address. The browser does not reload an address
  // it already has, so no error comes for it. That answer is the same failure, not a wait.
  it("treats a fresh link with the address that failed as the same failure, not a wait", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: ORIGINAL }));
    const onFail = vi.fn<(f: MediaFailure) => void>();
    const { container } = render(<MediaPlayer kind="video" name="broken.mp4" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0);
    fail(el, 4);
    await waitFor(() => expect(onFail).toHaveBeenCalledWith({ kind: "unsupported" }));
    expect(mint).toHaveBeenCalledTimes(1);
    expect(el.getAttribute("src")).toBe(ORIGINAL);
  });

  it("says a network failure on a fresh link is an error, not a format, and asks for nothing", async () => {
    const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="audio" name="a.mp3" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("audio") as HTMLAudioElement;
    at(el, 0);
    fail(el, 2);
    await waitFor(() => expect(el.getAttribute("src")).toBe(FRESH));
    fireEvent.loadedMetadata(el);
    fail(el, 2);
    expect(onFail).toHaveBeenCalledWith({ kind: "error", err: null });
    expect(mint).toHaveBeenCalledTimes(1);
  });

  it("reports a link that could not be renewed as an error, and keeps the first address", async () => {
    const err = new Error("gone");
    const mint = vi.fn(async (): Promise<Link> => {
      throw err;
    });
    const onFail = vi.fn();
    const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0);
    fail(el, 4);
    await waitFor(() => expect(onFail).toHaveBeenCalledWith({ kind: "error", err }));
    expect(container.querySelector("video")?.getAttribute("src")).toBe(ORIGINAL);
  });

  it("drops a link that comes back after the player has moved on", async () => {
    let answer: (l: Link) => void = () => {};
    const mint = vi.fn(() => new Promise<Link>((resolve) => (answer = resolve)));
    const { container, rerender } = render(<MediaPlayer key="a" kind="video" name="a.mp4" link={{ url: "https://content.example/a" }} mint={mint} onFail={() => {}} />);
    fail(container.querySelector("video") as HTMLVideoElement, 4);
    await waitFor(() => expect(mint).toHaveBeenCalled());
    // The person moves to another file: the player for it is keyed by the file, so the first one unmounts.
    rerender(<MediaPlayer key="b" kind="video" name="b.mp4" link={{ url: "https://content.example/b" }} mint={mint} onFail={() => {}} />);
    answer({ url: "https://content.example/a-fresh" });
    await new Promise((r) => setTimeout(r, 10));
    const shown = container.querySelector("video");
    expect(shown?.getAttribute("aria-label")).toBe("b.mp4");
    expect(shown?.getAttribute("src")).toBe("https://content.example/b");
  });

  it("pauses when it goes, so nothing keeps playing behind the next file", () => {
    const { unmount } = render(<MediaPlayer kind="audio" name="a.mp3" link={{ url: ORIGINAL }} mint={vi.fn()} onFail={() => {}} />);
    pause.mockClear();
    unmount();
    expect(pause).toHaveBeenCalled();
  });

  // The watchdog: a stall while playing waits STALL_MS (4 s) and then asks for a link. A stall that clears first asks for
  // nothing. Once the links for stalls are spent, the player keeps buffering and reports no error.
  describe("the stall watchdog", () => {
    it("asks for a link when playback stalls for four seconds, and not before", async () => {
      vi.useFakeTimers();
      const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
      const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={() => {}} />);
      const el = container.querySelector("video") as HTMLVideoElement;
      at(el, 5);
      fireEvent.waiting(el);
      await act(async () => {
        vi.advanceTimersByTime(3999);
      });
      expect(mint).not.toHaveBeenCalled();
      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      expect(mint).toHaveBeenCalledTimes(1);
    });

    it("asks for nothing when the stall clears before the watchdog fires", async () => {
      vi.useFakeTimers();
      const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
      const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={() => {}} />);
      const el = container.querySelector("video") as HTMLVideoElement;
      at(el, 5);
      fireEvent.waiting(el);
      fireEvent.playing(el);
      await act(async () => {
        vi.advanceTimersByTime(10_000);
      });
      expect(mint).not.toHaveBeenCalled();
    });

    it("never reports an error for a stall, even when the links for stalls are spent", async () => {
      vi.useFakeTimers();
      const mint = vi.fn(async (): Promise<Link> => ({ url: FRESH }));
      const onFail = vi.fn<(f: MediaFailure) => void>();
      const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
      const el = container.querySelector("video") as HTMLVideoElement;
      at(el, 5);
      // Four stalls, all at the same place: the first three ask for a link, the fourth asks for nothing.
      for (let i = 0; i < MAX_RENEWALS + 1; i++) {
        fireEvent.waiting(el);
        await act(async () => {
          vi.advanceTimersByTime(4000);
          await Promise.resolve();
        });
      }
      expect(mint).toHaveBeenCalledTimes(MAX_RENEWALS);
      expect(onFail).not.toHaveBeenCalled();
    });

    it("does not report an error when a stall's new link cannot be had", async () => {
      vi.useFakeTimers();
      const mint = vi.fn(async (): Promise<Link> => {
        throw new Error("no link");
      });
      const onFail = vi.fn();
      const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={mint} onFail={onFail} />);
      const el = container.querySelector("video") as HTMLVideoElement;
      at(el, 5);
      fireEvent.waiting(el);
      await act(async () => {
        vi.advanceTimersByTime(4000);
        await Promise.resolve();
      });
      expect(mint).toHaveBeenCalledTimes(1);
      expect(onFail).not.toHaveBeenCalled();
    });
  });

  it("gives its controls a left-to-right direction in a right-to-left page, and no mirror", () => {
    const { container } = render(<MediaPlayer kind="video" name="clip.webm" link={{ url: ORIGINAL }} mint={vi.fn()} onFail={() => {}} />);
    expect(container.querySelector("video")?.getAttribute("dir")).toBe("ltr");
  });
});
