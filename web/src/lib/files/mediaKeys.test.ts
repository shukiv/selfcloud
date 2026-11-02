// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { bufferedEnd, clamp, formatTime, ignoredTarget, isPopupTarget, keyCommand } from "./mediaKeys";

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

// The playlist drawer (slice x6, decision 63): an arrow pressed in the drawer moves its own list or field, never the player,
// and a key pressed in a dialog belongs to the dialog.
describe("ignoredTarget in the drawer and in a dialog", () => {
  // nest builds a chain of elements, each inside the one before, and returns the innermost.
  function nest(...chain: Array<[string, Record<string, string>?]>): HTMLElement {
    let parent: HTMLElement | null = null;
    let last: HTMLElement | null = null;
    for (const [tag, attrs] of chain) {
      const e = document.createElement(tag);
      for (const [k, v] of Object.entries(attrs ?? {})) e.setAttribute(k, v);
      (parent ?? document.body).appendChild(e);
      parent = e;
      last = e;
    }
    if (last === null) throw new Error("empty chain");
    return last;
  }

  it("leaves the arrows to the drawer's rows and buttons", () => {
    const b = nest(["div", { "data-media-drawer": "" }], ["ol"], ["li"], ["button"]);
    expect(ignoredTarget(b, "ArrowDown")).toBe(true);
    expect(ignoredTarget(b, "ArrowLeft")).toBe(true);
  });

  it("leaves every key pressed in a dialog to the dialog", () => {
    const b = nest(["div", { role: "dialog" }], ["button"]);
    expect(ignoredTarget(b, "ArrowRight")).toBe(true);
    expect(ignoredTarget(b, "k")).toBe(true);
  });

  it("still lets the arrows through when the focus is on the player's own bar, outside the drawer", () => {
    const b = nest(["div", { "data-testid": "media-stage" }], ["button"]);
    expect(ignoredTarget(b, "ArrowRight")).toBe(false);
  });

  it("does not take the viewer's keys: the viewer is a dialog too, and it is the player's page", () => {
    const b = nest(["div", { role: "dialog", "data-testid": "preview" }], ["div", { "data-testid": "media-stage" }], ["button"]);
    expect(ignoredTarget(b, "ArrowRight")).toBe(false);
    expect(ignoredTarget(b, "k")).toBe(false);
  });
});

describe("isPopupTarget", () => {
  // inside builds a parent with the given attributes and one child, and returns the child: the key's target.
  function inside(attrs: Record<string, string>, childId = "x"): HTMLElement {
    const parent = document.createElement("div");
    for (const [k, v] of Object.entries(attrs)) parent.setAttribute(k, v);
    const child = document.createElement("button");
    child.id = childId;
    parent.appendChild(child);
    document.body.replaceChildren(parent);
    return child;
  }

  it("is a menu item, a listbox option, a dropdown or a dialog other than the viewer", () => {
    expect(isPopupTarget(inside({ role: "menu" }))).toBe(true);
    expect(isPopupTarget(inside({ role: "listbox" }))).toBe(true);
    expect(isPopupTarget(inside({ class: "ant-dropdown" }))).toBe(true);
    expect(isPopupTarget(inside({ role: "dialog", "data-testid": "other" }))).toBe(true);
  });

  it("is not the viewer itself, nor a plain control or no target", () => {
    expect(isPopupTarget(inside({ role: "dialog", "data-testid": "preview" }))).toBe(false);
    expect(isPopupTarget(inside({}))).toBe(false);
    expect(isPopupTarget(null)).toBe(false);
  });
});
