// SPDX-License-Identifier: AGPL-3.0-or-later

// The place a player keeps: nothing is saved while the place is unknown, a place that arrives late is applied while the
// file is in its first seconds, and a failed read saves nothing until the person moves the playhead.

import { fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Resume } from "../../lib/files/resume";
import MediaPlayer, { type Link } from "./MediaPlayer";

const LINK: Link = { url: "https://content.example/clip.webm" };
const mint = async () => LINK;

// setup gives the element a time, a length and the data a browser has once it reports its metadata.
function setup(el: HTMLMediaElement, seconds: number, duration = 100) {
  Object.defineProperty(el, "currentTime", { value: seconds, writable: true, configurable: true });
  Object.defineProperty(el, "duration", { value: duration, configurable: true });
  Object.defineProperty(el, "paused", { value: false, configurable: true });
  Object.defineProperty(el, "readyState", { value: 1, configurable: true });
}

function renderWith(resume: Resume, onSave: (p: number, d: number) => void) {
  return render(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} resume={resume} onSave={onSave} autoPlay={false} />);
}

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("a place is saved only when it is known", () => {
  it("saves nothing while the place is still being read, not even on a tick or a pause", () => {
    const onSave = vi.fn();
    const { container } = renderWith({ kind: "pending" }, onSave);
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 12);
    fireEvent.timeUpdate(el);
    fireEvent.pause(el);
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves once the place is known to be none", () => {
    const onSave = vi.fn();
    const { container, rerender } = renderWith({ kind: "pending" }, onSave);
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 12);
    rerender(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} resume={{ kind: "none" }} onSave={onSave} autoPlay={false} />);
    fireEvent.timeUpdate(el);
    expect(onSave).toHaveBeenCalledWith(12, 100);
  });

  it("after a failed read, saves nothing until the person seeks", () => {
    const onSave = vi.fn();
    const { container } = renderWith({ kind: "unknown" }, onSave);
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 12);
    fireEvent.timeUpdate(el);
    fireEvent.pause(el);
    expect(onSave).not.toHaveBeenCalled();
    fireEvent.seeking(el);
    fireEvent.timeUpdate(el);
    expect(onSave).toHaveBeenCalled();
  });
});

describe("a place that arrives late", () => {
  it("applies while the file is in its first seconds and the person has not moved the playhead", () => {
    const { container, rerender } = renderWith({ kind: "pending" }, () => {});
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 0);
    fireEvent.loadedMetadata(el);
    rerender(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} resume={{ kind: "place", position: 42, duration: 100 }} onSave={() => {}} autoPlay={false} />);
    expect(el.currentTime).toBe(42);
  });

  it("is not applied once the person has moved the playhead", () => {
    const { container, rerender } = renderWith({ kind: "pending" }, () => {});
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 0);
    fireEvent.loadedMetadata(el);
    fireEvent.seeking(el);
    setup(el, 3);
    rerender(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} resume={{ kind: "place", position: 42, duration: 100 }} onSave={() => {}} autoPlay={false} />);
    expect(el.currentTime).toBe(3);
  });

  it("is not applied after ten seconds of play", () => {
    const { container, rerender } = renderWith({ kind: "pending" }, () => {});
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 15);
    fireEvent.loadedMetadata(el);
    rerender(<MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} resume={{ kind: "place", position: 42, duration: 100 }} onSave={() => {}} autoPlay={false} />);
    expect(el.currentTime).toBe(15);
  });

  it("is applied at the first load when it is already known", () => {
    const { container } = renderWith({ kind: "place", position: 42, duration: 100 }, () => {});
    const el = container.querySelector("video") as HTMLVideoElement;
    setup(el, 0);
    fireEvent.loadedMetadata(el);
    expect(el.currentTime).toBe(42);
  });
});
