// SPDX-License-Identifier: AGPL-3.0-or-later

// The speed list's input is a combobox, not a field of text: a key pressed with it focused is the player's, as the list
// itself showed (it ate the keys once it had focus). A rename field still keeps them.

import { fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import MediaPlayer from "./MediaPlayer";

const LINK = { url: "https://content.example/clip.webm" };
const mint = vi.fn(async () => LINK);

let play: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("keys with the speed list focused", () => {
  it("are the player's: a combobox input does not take the keys", () => {
    const { container } = render(
      <>
        <MediaPlayer kind="video" name="clip.webm" link={LINK} mint={mint} onFail={() => {}} />
        <input role="combobox" aria-label="speed" />
      </>,
    );
    const combo = container.querySelector('input[role="combobox"]') as HTMLInputElement;
    fireEvent.keyDown(combo, { key: " " });
    expect(play).toHaveBeenCalledTimes(1);
  });
});
