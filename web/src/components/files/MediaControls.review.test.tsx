// SPDX-License-Identifier: AGPL-3.0-or-later

// The bar's full screen and picture-in-picture state: the button says what the next press does, and it follows the
// browser when the state changes by any other means.

import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import MediaControls from "./MediaControls";

function defineDoc(prop: string, value: unknown) {
  Object.defineProperty(document, prop, { configurable: true, get: () => value });
}

afterEach(() => {
  delete (document as unknown as Record<string, unknown>).fullscreenElement;
  delete (document as unknown as Record<string, unknown>).pictureInPictureElement;
  delete (document as unknown as Record<string, unknown>).pictureInPictureEnabled;
});

describe("the bar's full screen and picture-in-picture state", () => {
  it("says Exit full screen while the stage is the full screen element, and follows the browser's change", () => {
    const stage = document.createElement("div");
    document.body.appendChild(stage);
    const media = document.createElement("video");
    render(<MediaControls media={media} kind="video" stage={stage} />);
    expect(screen.getByRole("button", { name: "Full screen" })).toBeTruthy();
    // The browser takes the stage full screen by any means (the Escape key, another control).
    defineDoc("fullscreenElement", stage);
    act(() => {
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    expect(screen.getByRole("button", { name: "Exit full screen" })).toBeTruthy();
    defineDoc("fullscreenElement", null);
    act(() => {
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    expect(screen.getByRole("button", { name: "Full screen" })).toBeTruthy();
    stage.remove();
  });

  it("says Leave picture in picture while the video is in one, and follows the element's event", () => {
    defineDoc("pictureInPictureEnabled", true);
    const media = document.createElement("video");
    render(<MediaControls media={media} kind="video" stage={null} />);
    expect(screen.getByRole("button", { name: "Picture in picture" })).toBeTruthy();
    defineDoc("pictureInPictureElement", media);
    act(() => {
      media.dispatchEvent(new Event("enterpictureinpicture"));
    });
    expect(screen.getByRole("button", { name: "Leave picture in picture" }).getAttribute("aria-pressed")).toBe("true");
    defineDoc("pictureInPictureElement", null);
    act(() => {
      media.dispatchEvent(new Event("leavepictureinpicture"));
    });
    expect(screen.getByRole("button", { name: "Picture in picture" }).getAttribute("aria-pressed")).toBe("false");
  });

  it("has no full screen or picture-in-picture button on audio", () => {
    defineDoc("pictureInPictureEnabled", true);
    render(<MediaControls media={document.createElement("audio")} kind="audio" stage={null} />);
    expect(screen.queryByRole("button", { name: "Full screen" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Picture in picture" })).toBeNull();
  });
});
