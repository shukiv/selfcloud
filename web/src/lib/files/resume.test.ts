// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { RESUME_FROM_S, RESUME_UNTIL_END_S, SAVE_EVERY_MS, SaveClock, startAt } from "./resume";

describe("startAt: where a file starts, given the saved place", () => {
  it("starts at the beginning with no saved place", () => {
    expect(startAt(null, 100)).toBe(0);
  });

  it("starts at the beginning when the place is nearer the start than RESUME_FROM_S", () => {
    expect(startAt({ position: RESUME_FROM_S - 0.5, duration: 100 }, 100)).toBe(0);
    expect(startAt({ position: RESUME_FROM_S, duration: 100 }, 100)).toBe(RESUME_FROM_S);
  });

  it("starts at the beginning when the place is within RESUME_UNTIL_END_S of the end", () => {
    expect(startAt({ position: 100 - RESUME_UNTIL_END_S, duration: 100 }, 100)).toBe(0);
    expect(startAt({ position: 100 - RESUME_UNTIL_END_S - 1, duration: 100 }, 100)).toBe(89);
  });

  it("starts at the place when the length is unknown, and the place is not near a known end", () => {
    expect(startAt({ position: 30, duration: 0 }, 0)).toBe(30);
  });

  it("starts at the beginning for a place that is not a finite number", () => {
    expect(startAt({ position: Number.NaN, duration: 100 }, 100)).toBe(0);
    expect(startAt({ position: Number.POSITIVE_INFINITY, duration: 100 }, 100)).toBe(0);
  });
});

describe("SaveClock: when a place is written", () => {
  it("writes on the first tick, then at most once in SAVE_EVERY_MS", () => {
    const c = new SaveClock();
    expect(c.tick(0, 10)).toBe(true);
    expect(c.tick(SAVE_EVERY_MS - 1, 11)).toBe(false);
    expect(c.tick(SAVE_EVERY_MS, 12)).toBe(true);
  });

  it("writes on a pause when the place has moved since the last write, whatever the time", () => {
    const c = new SaveClock();
    c.tick(0, 10);
    expect(c.pause(1, 10.2)).toBe(false);
    expect(c.pause(2, 14)).toBe(true);
  });

  it("writes nothing on a pause with no movement since the last write", () => {
    const c = new SaveClock();
    c.tick(0, 10);
    expect(c.pause(1, 10)).toBe(false);
  });

  it("writes the beginning when the file ends, and then waits for the clock as usual", () => {
    const c = new SaveClock();
    c.tick(0, 50);
    expect(c.ended(1)).toBe(true);
    expect(c.tick(2, 0.1)).toBe(false);
    expect(c.pause(3, 0)).toBe(false);
  });
});
