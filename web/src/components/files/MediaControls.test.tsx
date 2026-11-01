// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HIDE_MS, VOLUME_STEP, playerPrefs } from "../../lib/files/mediaKeys";
import MediaPlayer from "./MediaPlayer";

// at sets where playback is, and whether it is paused, as the element reports them.
function at(el: HTMLMediaElement, seconds: number, paused = true) {
  Object.defineProperty(el, "currentTime", { value: seconds, writable: true, configurable: true });
  Object.defineProperty(el, "paused", { value: paused, configurable: true });
}

const LINK = { url: "https://content.example/clip.webm" };
const mint = async () => LINK;

describe("MediaControls", () => {
  let play: ReturnType<typeof vi.spyOn>;
  let pause: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
    playerPrefs.volume = 1;
    playerPrefs.muted = false;
    playerPrefs.rate = 1;
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("replaces the browser's controls: the element has none, and every icon button has a label", () => {
    const { container, getAllByRole } = render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    expect(el.hasAttribute("controls")).toBe(false);
    const buttons = getAllByRole("button");
    expect(buttons.length).toBeGreaterThan(0);
    // Each icon button names itself: the label is the accessible name, and it is not empty.
    for (const b of buttons) expect((b.getAttribute("aria-label") ?? "").trim().length).toBeGreaterThan(0);
  });

  it("toggles play with Space and K, and leaves the keys to a field that is typed in", () => {
    const { container } = render(
      <>
        <MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />
        <input aria-label="rename" />
      </>,
    );
    // The rename field, by its label: the speed list's own input (a combobox) comes first in the bar and is not a field.
    const field = container.querySelector('input[aria-label="rename"]') as HTMLInputElement;
    fireEvent.keyDown(field, { key: " " });
    fireEvent.keyDown(field, { key: "k" });
    expect(play).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: " " });
    expect(play).toHaveBeenCalledTimes(1);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0, false);
    fireEvent.keyDown(document.body, { key: "k" });
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it("seeks with the arrows and with J and L, and keeps the volume the person set for the next file", () => {
    const { container, unmount } = render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    Object.defineProperty(el, "duration", { value: 100, configurable: true });
    at(el, 20);
    fireEvent.keyDown(document.body, { key: "ArrowRight" });
    expect(el.currentTime).toBe(25);
    fireEvent.keyDown(document.body, { key: "j" });
    expect(el.currentTime).toBe(15);
    fireEvent.keyDown(document.body, { key: "L" });
    expect(el.currentTime).toBe(25);
    el.volume = 0.5;
    fireEvent.keyDown(document.body, { key: "ArrowDown" });
    expect(el.volume).toBeCloseTo(0.5 - VOLUME_STEP);
    unmount();
    // The next file starts at the volume the person chose.
    const next = render(<MediaPlayer kind="video" name="next.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const again = next.container.querySelector("video") as HTMLVideoElement;
    expect(again.volume).toBeCloseTo(0.5 - VOLUME_STEP);
  });

  it("mutes with M, and keeps the mute for the next file", () => {
    const { container, unmount } = render(<MediaPlayer kind="audio" name="song.mp3" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("audio") as HTMLAudioElement;
    fireEvent.keyDown(document.body, { key: "m" });
    expect(el.muted).toBe(true);
    unmount();
    const next = render(<MediaPlayer kind="audio" name="song2.mp3" link={LINK} mint={mint} onFail={() => {}} />);
    expect((next.container.querySelector("audio") as HTMLAudioElement).muted).toBe(true);
  });

  it("does not take a key that carries a modifier: Ctrl+F is the browser's", () => {
    render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const prevented = vi.fn();
    const e = new KeyboardEvent("keydown", { key: "f", ctrlKey: true, cancelable: true });
    e.preventDefault = prevented;
    window.dispatchEvent(e);
    expect(prevented).not.toHaveBeenCalled();
  });

  it("hides the bar of a playing video, shows it on a pointer move, and keeps it while focus is in it", () => {
    vi.useFakeTimers();
    const { container, getByTestId } = render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0, false);
    act(() => {
      fireEvent.play(el);
    });
    const bar = getByTestId("media-controls");
    act(() => {
      vi.advanceTimersByTime(HIDE_MS + 10);
    });
    expect(bar.className).toContain("media-controls--hidden");
    const stage = getByTestId("media-stage");
    fireEvent.pointerMove(stage);
    expect(bar.className).not.toContain("media-controls--hidden");
    act(() => {
      vi.advanceTimersByTime(HIDE_MS + 10);
    });
    // Keyboard focus in the bar: it stays, however long the video plays.
    const button = bar.querySelector("button") as HTMLButtonElement;
    act(() => {
      button.focus();
    });
    act(() => {
      vi.advanceTimersByTime(HIDE_MS * 3);
    });
    expect(bar.className).not.toContain("media-controls--hidden");
  });

  it("never hides the bar of audio", () => {
    vi.useFakeTimers();
    const { container, getByTestId } = render(<MediaPlayer kind="audio" name="song.mp3" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("audio") as HTMLAudioElement;
    at(el, 0, false);
    act(() => {
      fireEvent.play(el);
    });
    act(() => {
      vi.advanceTimersByTime(HIDE_MS * 2);
    });
    expect(getByTestId("media-controls").className).not.toContain("media-controls--hidden");
  });
});
