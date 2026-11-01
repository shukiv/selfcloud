// SPDX-License-Identifier: AGPL-3.0-or-later

// The bar's keys and its focus: a viewer that is closed reads no key, and keyboard focus in the bar keeps it shown.

import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HIDE_MS } from "../../lib/files/mediaKeys";
import MediaControls from "./MediaControls";

function playing(el: HTMLMediaElement) {
  Object.defineProperty(el, "paused", { value: false, configurable: true });
  Object.defineProperty(el, "currentTime", { value: 3, writable: true, configurable: true });
}

let play: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("MediaControls keys when the viewer is closed", () => {
  it("reads no key while its viewer is closed, so a Space press on the page does not play the audio", () => {
    const el = document.createElement("audio");
    render(<MediaControls media={el} kind="audio" stage={null} active={false} />);
    fireEvent.keyDown(window, { key: " " });
    expect(play).not.toHaveBeenCalled();
  });

  it("reads the keys while its viewer is open", () => {
    const el = document.createElement("audio");
    Object.defineProperty(el, "paused", { value: true, configurable: true });
    render(<MediaControls media={el} kind="audio" stage={null} active />);
    fireEvent.keyDown(window, { key: " " });
    expect(play).toHaveBeenCalledTimes(1);
  });
});

describe("keyboard focus in a playing video's bar keeps it shown", () => {
  it("keeps the bar shown after its count, when the focus came from the keyboard", () => {
    vi.useFakeTimers();
    const el = document.createElement("video");
    const { container, getByTestId } = render(<MediaControls media={el} kind="video" stage={null} />);
    playing(el);
    act(() => {
      fireEvent.play(el);
    });
    const bar = getByTestId("media-controls");
    const button = bar.querySelector("button") as HTMLButtonElement;
    act(() => {
      button.focus();
    });
    act(() => {
      vi.advanceTimersByTime(HIDE_MS + 10);
    });
    expect(bar.className).not.toContain("media-controls--hidden");
    expect(container).toBeTruthy();
  });

  it("hides the bar after its count when the focus has left it", () => {
    vi.useFakeTimers();
    const el = document.createElement("video");
    const { getByTestId } = render(<MediaControls media={el} kind="video" stage={null} />);
    playing(el);
    act(() => {
      fireEvent.play(el);
    });
    const bar = getByTestId("media-controls");
    const button = bar.querySelector("button") as HTMLButtonElement;
    act(() => {
      button.focus();
    });
    act(() => {
      button.blur();
    });
    act(() => {
      vi.advanceTimersByTime(HIDE_MS + 10);
    });
    expect(bar.className).toContain("media-controls--hidden");
  });
});
