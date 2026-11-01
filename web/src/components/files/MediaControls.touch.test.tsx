// SPDX-License-Identifier: AGPL-3.0-or-later

// A tap on a button of the bar gives the button focus on a phone, but it is not keyboard focus: the bar still hides while
// the video plays. Keyboard focus inside the bar keeps it shown (MediaControls.test.tsx).

import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HIDE_MS } from "../../lib/files/mediaKeys";
import MediaPlayer from "./MediaPlayer";

function at(el: HTMLMediaElement, seconds: number, paused = true) {
  Object.defineProperty(el, "currentTime", { value: seconds, writable: true, configurable: true });
  Object.defineProperty(el, "paused", { value: paused, configurable: true });
}

const LINK = { url: "https://content.example/clip.webm" };
const mint = async () => LINK;

afterEach(() => {
  vi.useRealTimers();
});

describe("MediaControls on a touch screen", () => {
  it("hides the bar after a tap on one of its buttons, while the video plays", () => {
    vi.useFakeTimers();
    const { container, getByTestId } = render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />);
    const el = container.querySelector("video") as HTMLVideoElement;
    at(el, 0, false);
    act(() => {
      fireEvent.play(el);
    });
    const bar = getByTestId("media-controls");
    const button = bar.querySelector("button") as HTMLButtonElement;
    // The tap: a pointer press on the button (it bubbles to the picture), then the browser's focus on the button.
    act(() => {
      fireEvent.pointerDown(button);
      button.focus();
    });
    act(() => {
      vi.advanceTimersByTime(HIDE_MS + 10);
    });
    expect(bar.className).toContain("media-controls--hidden");
  });
});
