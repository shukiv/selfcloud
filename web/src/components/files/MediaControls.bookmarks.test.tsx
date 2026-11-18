// SPDX-License-Identifier: AGPL-3.0-or-later

// The seek bar's bookmark ticks: one mark per bookmark at its place in the file, a mark only (it takes no pointer), and
// none without marks or a known length. Jumping to a place is the list's Go to, not a tick.

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Bookmark } from "../../lib/files/api";
import type { Marks } from "../../lib/files/bookmarks";
import { playerPrefs } from "../../lib/files/mediaKeys";
import MediaControls from "./MediaControls";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({
  id: "bmk_1",
  position_seconds: 25,
  label: "",
  created_at: "2026-11-16T00:00:00Z",
  ...over,
});

function marks(items: Bookmark[]): Marks {
  return { items, ready: true, full: false, add: vi.fn(), openList: vi.fn() };
}

function el(duration: number): HTMLMediaElement {
  const m = document.createElement("audio");
  Object.defineProperty(m, "duration", { value: duration, configurable: true });
  Object.defineProperty(m, "currentTime", { value: 0, writable: true, configurable: true });
  return m;
}

beforeEach(() => {
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  playerPrefs.volume = 1;
  playerPrefs.muted = false;
  playerPrefs.rate = 1;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the seek bar's bookmark ticks", () => {
  it("draws one tick per bookmark at its percent of the length, hidden from the reader and the pointer", async () => {
    render(<MediaControls media={el(100)} kind="audio" stage={null} marks={marks([b({ id: "bmk_a", position_seconds: 25 }), b({ id: "bmk_b", position_seconds: 50 })])} />);
    const ticks = await screen.findAllByTestId("media-bookmark-tick");
    expect(ticks.length).toBe(2);
    expect(ticks[0].style.insetInlineStart).toBe("25%");
    expect(ticks[1].style.insetInlineStart).toBe("50%");
    for (const t of ticks) {
      expect(t.getAttribute("aria-hidden")).toBe("true");
      expect(getComputedStyle(t).pointerEvents).toBe("none");
    }
  });

  it("draws no tick without marks", async () => {
    render(<MediaControls media={el(100)} kind="audio" stage={null} />);
    // Give the bar a moment to read the element, then judge that no tick was drawn.
    await screen.findByTestId("media-controls");
    expect(screen.queryAllByTestId("media-bookmark-tick")).toEqual([]);
  });

  it("draws no tick while the length is unknown", async () => {
    render(<MediaControls media={el(0)} kind="audio" stage={null} marks={marks([b()])} />);
    await screen.findByTestId("media-controls");
    expect(screen.queryAllByTestId("media-bookmark-tick")).toEqual([]);
  });
});
