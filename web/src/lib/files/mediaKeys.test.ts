// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { bufferedEnd, clamp, formatTime, ignoredTarget, keyCommand } from "./mediaKeys";

describe("keyCommand", () => {
  it.each([
    [" ", "toggle"],
    ["k", "toggle"],
    ["K", "toggle"],
    ["j", "back10"],
    ["L", "forward10"],
    ["ArrowLeft", "back5"],
    ["ArrowRight", "forward5"],
    ["ArrowUp", "volumeUp"],
    ["ArrowDown", "volumeDown"],
    ["m", "mute"],
    ["F", "fullscreen"],
    ["n", "next"],
    ["P", "previous"],
  ])("maps %s to %s", (key, want) => {
    expect(keyCommand({ key })).toBe(want);
  });

  it("means nothing for other keys, and for a press with a modifier", () => {
    expect(keyCommand({ key: "x" })).toBeNull();
    expect(keyCommand({ key: "f", ctrlKey: true })).toBeNull();
    expect(keyCommand({ key: "ArrowLeft", altKey: true })).toBeNull();
    expect(keyCommand({ key: "m", metaKey: true })).toBeNull();
  });
});

// A plain element stands in for a control: the closest role decides what the key means to it.
function el(tag: string, role?: string, editable = false): HTMLElement {
  const node = document.createElement(tag);
  if (role) node.setAttribute("role", role);
  if (editable) node.setAttribute("contenteditable", "true");
  document.body.appendChild(node);
  return node;
}

describe("ignoredTarget", () => {
  it("leaves every key typed into a field to the field", () => {
    expect(ignoredTarget(el("input"), "k")).toBe(true);
    expect(ignoredTarget(el("textarea"), "n")).toBe(true);
    expect(ignoredTarget(el("div", undefined, true), "m")).toBe(true);
  });

  it("does not stop the player's keys when the focus is on the player or a plain element", () => {
    expect(ignoredTarget(document.body, "k")).toBe(false);
    expect(ignoredTarget(el("div"), "f")).toBe(false);
  });

  it("leaves arrows to a slider or a menu", () => {
    expect(ignoredTarget(el("span", "slider"), "ArrowLeft")).toBe(true);
    // A combobox (the speed list) takes no keys of its own: the player's keys are read there, as in any control that is not a field.
    expect(ignoredTarget(el("span", "combobox"), "ArrowDown")).toBe(false);
  });

  // kbEl is an element the keyboard holds the focus on (Tab brought it), as :focus-visible says in a browser.
  function kbEl(tag: string, role?: string): HTMLElement {
    const e = el(tag, role);
    const matches = e.matches.bind(e);
    e.matches = ((s: string) => (s === ":focus-visible" ? true : matches(s))) as typeof e.matches;
    return e;
  }

  it("gives Space to a control only where the keyboard holds the focus on it", () => {
    // Reached by Tab: the control's own Space (pressed, followed, opened).
    expect(ignoredTarget(kbEl("button"), " ")).toBe(true);
    expect(ignoredTarget(kbEl("a"), " ")).toBe(true);
    expect(ignoredTarget(kbEl("span", "button"), " ")).toBe(true);
    expect(ignoredTarget(kbEl("span", "combobox"), " ")).toBe(true);
    // Reached by a press of the pointer: the focus is on the control, but not the keyboard's, so Space plays or pauses.
    expect(ignoredTarget(el("button"), " ")).toBe(false);
    expect(ignoredTarget(el("a"), " ")).toBe(false);
    expect(ignoredTarget(el("span", "combobox"), "Spacebar")).toBe(false);
    // A slider does not use Space: it is the player's even with the keyboard on it.
    expect(ignoredTarget(kbEl("span", "slider"), " ")).toBe(false);
  });

  it("keeps the player's own keys on a button: K, N and the rest are the player's", () => {
    expect(ignoredTarget(el("button"), "k")).toBe(false);
    expect(ignoredTarget(el("button"), "ArrowLeft")).toBe(false);
  });
});

describe("formatTime", () => {
  it("writes minutes and seconds, and hours from an hour on", () => {
    expect(formatTime(0)).toBe("0:00");
    expect(formatTime(65.9)).toBe("1:05");
    expect(formatTime(3725)).toBe("1:02:05");
  });

  it("writes 0:00 for a time that is not a number yet", () => {
    expect(formatTime(Number.NaN)).toBe("0:00");
    expect(formatTime(Number.POSITIVE_INFINITY)).toBe("0:00");
    expect(formatTime(-4)).toBe("0:00");
  });
});

describe("bufferedEnd", () => {
  const ranges = (...pairs: Array<[number, number]>) => ({
    length: pairs.length,
    start: (i: number) => pairs[i][0],
    end: (i: number) => pairs[i][1],
  });

  it("is where the range that holds the place ends", () => {
    expect(bufferedEnd(ranges([0, 10], [50, 80]), 5)).toBe(10);
    expect(bufferedEnd(ranges([0, 10], [50, 80]), 60)).toBe(80);
  });

  it("is where the last range ends when the place is in none of them", () => {
    expect(bufferedEnd(ranges([0, 10], [50, 80]), 30)).toBe(80);
    expect(bufferedEnd(ranges(), 30)).toBe(0);
  });
});

describe("clamp", () => {
  it("keeps a value in its range", () => {
    expect(clamp(-1, 0, 1)).toBe(0);
    expect(clamp(0.4, 0, 1)).toBe(0.4);
    expect(clamp(3, 0, 1)).toBe(1);
  });
});
