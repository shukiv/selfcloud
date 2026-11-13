// SPDX-License-Identifier: AGPL-3.0-or-later

// The player, on the element: the iPhone's full screen where the page cannot take the stage full
// screen, the playsInline attribute that keeps a video on its place on an iPhone, and the volume and the mute written to
// the browser's storage only when they change (not on each time update).

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import MediaControls from "./MediaControls";
import MediaPlayer from "./MediaPlayer";
import { playerPrefs } from "../../lib/files/mediaKeys";

afterEach(() => {
  vi.restoreAllMocks();
  playerPrefs.volume = 1;
  playerPrefs.muted = false;
});

describe("full screen on a device without the stage's full screen", () => {
  it("asks the video element for its own full screen (iPhone)", () => {
    const media = document.createElement("video") as HTMLVideoElement & { webkitEnterFullscreen?: () => void };
    media.webkitEnterFullscreen = vi.fn();
    render(<MediaControls media={media} kind="video" stage={null} />);
    fireEvent.click(screen.getByRole("button", { name: "Full screen" }));
    expect(media.webkitEnterFullscreen).toHaveBeenCalledTimes(1);
  });

  it("does nothing on audio, which has no picture to take full screen", () => {
    const media = document.createElement("audio") as HTMLAudioElement & { webkitEnterFullscreen?: () => void };
    media.webkitEnterFullscreen = vi.fn();
    render(<MediaControls media={media} kind="audio" stage={null} />);
    expect(screen.queryByRole("button", { name: "Full screen" })).toBeNull();
    expect(media.webkitEnterFullscreen).not.toHaveBeenCalled();
  });
});

describe("the volume and the mute are kept only when they change", () => {
  it("writes nothing on time updates, and one write for a volume change", () => {
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    const media = document.createElement("video");
    render(<MediaControls media={media} kind="video" stage={null} />);
    setItem.mockClear();
    for (let i = 0; i < 5; i++) {
      media.currentTime = i;
      act(() => {
        media.dispatchEvent(new Event("timeupdate"));
      });
    }
    expect(setItem).not.toHaveBeenCalled();
    media.volume = 0.4;
    act(() => {
      media.dispatchEvent(new Event("volumechange"));
    });
    expect(setItem).toHaveBeenCalledTimes(1);
    expect(setItem.mock.calls[0][0]).toBe("selfcloud.player");
    expect(JSON.parse(setItem.mock.calls[0][1] as string)).toEqual({ volume: 0.4, muted: false });
  });
});

describe("the keys a held key repeats", () => {
  function setup(nav?: { next: () => void; previous: () => void }) {
    const media = document.createElement("video");
    const play = vi.fn(() => Promise.resolve());
    const pause = vi.fn();
    media.play = play as unknown as HTMLMediaElement["play"];
    media.pause = pause;
    const utils = render(<MediaControls media={media} kind="video" stage={null} nav={nav} />);
    return { media, play, pause, ...utils };
  }

  it("does not toggle play again on a repeat of Space, and toggles once per press", () => {
    const { play, pause } = setup();
    fireEvent.keyDown(window, { key: " ", repeat: true });
    expect(play).not.toHaveBeenCalled();
    expect(pause).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: " " });
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("does not step to the next file on a repeat of N, and does on the first press", () => {
    const next = vi.fn();
    setup({ next, previous: vi.fn() });
    fireEvent.keyDown(window, { key: "n", repeat: true });
    expect(next).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "n" });
    expect(next).toHaveBeenCalledTimes(1);
  });

  it("seeks on a repeat of the right arrow, as a held key should", () => {
    const { media } = setup();
    media.currentTime = 0;
    fireEvent.keyDown(window, { key: "ArrowRight", repeat: true });
    expect(media.currentTime).toBe(5);
  });

  it("takes the Space keyup of a Space it handled, and leaves a keyup it did not handle", () => {
    setup();
    fireEvent.keyDown(window, { key: " " });
    // The keyup of the handled Space is cancelled, so a browser does not click the button the pointer last hit.
    expect(fireEvent.keyUp(window, { key: " " })).toBe(false);
    // A Space whose keydown the player did not take is left alone (here a Space typed into a field).
    const field = document.createElement("input");
    document.body.appendChild(field);
    fireEvent.keyDown(field, { key: " " });
    expect(fireEvent.keyUp(field, { key: " " })).toBe(true);
    field.remove();
  });
});

describe("a play the browser refuses for want of a press", () => {
  // The note is held by MediaPlayer, so the test mounts the player and patches the element it renders.
  function refusing(name: string) {
    const utils = render(
      <MediaPlayer kind="video" name="clip.webm" link={{ url: "https://content.example/clip" }} mint={async () => ({ url: "https://content.example/clip" })} onFail={() => undefined} />,
    );
    const media = utils.container.querySelector("video") as HTMLVideoElement;
    media.play = vi.fn(() => Promise.reject(Object.assign(new Error("refused"), { name }))) as unknown as HTMLMediaElement["play"];
    return media;
  }

  it("shows a note by the bar, and clears it when the file plays", async () => {
    const media = refusing("NotAllowedError");
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    expect(await screen.findByText("The browser waits for you to press Play.")).toBeTruthy();
    act(() => {
      media.dispatchEvent(new Event("play"));
    });
    expect(screen.queryByText("The browser waits for you to press Play.")).toBeNull();
  });

  it("shows no note for any other refusal to play", async () => {
    refusing("AbortError");
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.queryByText("The browser waits for you to press Play.")).toBeNull();
  });
});

describe("the player element", () => {
  it("is inline on a phone: a video has the playsinline attribute", () => {
    const { container } = render(
      <MediaPlayer kind="video" name="clip.webm" link={{ url: "https://content.example/clip" }} mint={async () => ({ url: "https://content.example/clip" })} onFail={() => undefined} />,
    );
    expect(container.querySelector("video")?.hasAttribute("playsinline")).toBe(true);
  });
});
