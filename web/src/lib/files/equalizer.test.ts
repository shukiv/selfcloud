// SPDX-License-Identifier: AGPL-3.0-or-later

// The equalizer choice: ten gains in dB, kept per person in this browser, clamped to -12..12 on the step of 1, with the
// default on any bad stored value. The presets name the gains that match them; custom names any others.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BANDS, DEFAULT_EQ, PRESETS, bandLabel, dbLabel, gainsOf, presetOf, readEq, writeEq, type Eq } from "./equalizer";

const KEY = "selfcloud.equalizer.u";

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe("the bands and the presets", () => {
  it("names the ten bands, 31 Hz to 16 kHz", () => {
    expect(BANDS).toEqual([31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000]);
  });

  it("holds the four presets", () => {
    expect(PRESETS.flat).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
    expect(PRESETS.bass).toEqual([6, 5, 4, 2, 0, 0, 0, 0, 0, 0]);
    expect(PRESETS.treble).toEqual([0, 0, 0, 0, 0, 0, 2, 4, 5, 6]);
    expect(PRESETS.vocal).toEqual([-2, -2, -1, 1, 3, 3, 2, 1, 0, -1]);
  });

  it("presetOf names the matching preset, else custom", () => {
    expect(presetOf(PRESETS.flat)).toBe("flat");
    expect(presetOf(PRESETS.bass)).toBe("bass");
    expect(presetOf(PRESETS.treble)).toBe("treble");
    expect(presetOf(PRESETS.vocal)).toBe("vocal");
    expect(presetOf([1, 0, 0, 0, 0, 0, 0, 0, 0, 0])).toBe("custom");
    expect(presetOf([0, 0, 0])).toBe("custom");
    expect(presetOf([])).toBe("custom");
  });

  it("the default is off with all ten at 0", () => {
    expect(DEFAULT_EQ).toEqual({ on: false, gains: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0] });
    expect(readEq("u")).toEqual(DEFAULT_EQ);
  });

  it("gainsOf is the choice's gains when on and all 0 when off", () => {
    expect(gainsOf({ on: true, gains: PRESETS.bass })).toEqual(PRESETS.bass);
    expect(gainsOf({ on: false, gains: PRESETS.bass })).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
  });

  it("labels the bands in Hz and kHz, and the gains in dB", () => {
    expect(BANDS.map(bandLabel)).toEqual(["31 Hz", "62 Hz", "125 Hz", "250 Hz", "500 Hz", "1 kHz", "2 kHz", "4 kHz", "8 kHz", "16 kHz"]);
    expect(dbLabel(3)).toBe("+3 dB");
    expect(dbLabel(0)).toBe("0 dB");
    expect(dbLabel(-2)).toBe("-2 dB");
  });
});

describe("the kept equalizer", () => {
  it("keeps what was written under the person's own key, and reads no one else's", () => {
    const eq: Eq = { on: true, gains: [...PRESETS.bass] };
    writeEq("u", eq);
    expect(window.localStorage.getItem(KEY)).not.toBeNull();
    expect(readEq("u")).toEqual(eq);
    expect(readEq("someone-else")).toEqual(DEFAULT_EQ);
  });

  it("keeps the visitor's choice under visitor", () => {
    const eq: Eq = { on: true, gains: [...PRESETS.vocal] };
    writeEq(null, eq);
    expect(window.localStorage.getItem("selfcloud.equalizer.visitor")).not.toBeNull();
    expect(readEq(null)).toEqual(eq);
    expect(readEq("u")).toEqual(DEFAULT_EQ);
  });

  it("clamps a stored gain to -12..12 and rounds it to the step of 1", () => {
    window.localStorage.setItem(KEY, JSON.stringify({ on: true, gains: [20, -20, 2.4, -2.6, 12, -12, 0, 0, 0, 0] }));
    expect(readEq("u")).toEqual({ on: true, gains: [12, -12, 2, -3, 12, -12, 0, 0, 0, 0] });
  });

  it("falls back to the default on any bad stored value", () => {
    const bad = [
      "not json",
      "",
      "null",
      "[]",
      "{}",
      JSON.stringify({ on: "yes", gains: PRESETS.flat }),
      JSON.stringify({ on: true, gains: [0, 0, 0] }),
      JSON.stringify({ on: true, gains: [0, 0, 0, 0, 0, 0, 0, 0, 0, "x"] }),
      JSON.stringify({ on: true, gains: [0, 0, 0, 0, 0, 0, 0, 0, 0, null] }),
      JSON.stringify({ on: true }),
      JSON.stringify({ gains: PRESETS.flat }),
    ];
    for (const raw of bad) {
      window.localStorage.setItem(KEY, raw);
      expect(readEq("u"), raw).toEqual(DEFAULT_EQ);
    }
  });

  it("writes nothing outside -12..12 or the step of 1, and no bad list", () => {
    writeEq("u", { on: true, gains: [500, -3.2, 0, 0, 0, 0, 0, 0, 0, 0] });
    expect(readEq("u")).toEqual({ on: true, gains: [12, -3, 0, 0, 0, 0, 0, 0, 0, 0] });
    writeEq("u", { on: true, gains: [1, 2, 3] });
    expect(readEq("u")).toEqual(DEFAULT_EQ);
    writeEq("u", { on: true, gains: [Number.NaN, 0, 0, 0, 0, 0, 0, 0, 0, 0] });
    expect(readEq("u")).toEqual(DEFAULT_EQ);
  });

  it("reads the default and throws nothing when storage refuses", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(readEq("u")).toEqual(DEFAULT_EQ);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => writeEq("u", DEFAULT_EQ)).not.toThrow();
  });
});
