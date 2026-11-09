// SPDX-License-Identifier: AGPL-3.0-or-later

import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useNewChord } from "../../components/files/SidebarNew";

// A key press on the page, as the browser delivers it (it bubbles from the focused element to the window listener).
function press(code: string, altKey = false) {
  document.body.dispatchEvent(new KeyboardEvent("keydown", { code, altKey, bubbles: true, cancelable: true }));
}

describe("useNewChord: the chord is cancelled by blur and by Escape (F6)", () => {
  afterEach(() => {
    document.body.replaceChildren();
  });

  it("Alt+C then a window blur, then F: the letter does not run the action", () => {
    const run = vi.fn();
    renderHook(() => useNewChord(() => true, run));
    press("KeyC", true);
    window.dispatchEvent(new Event("blur"));
    press("KeyF");
    expect(run).not.toHaveBeenCalled();
  });

  it("Alt+C then Escape, then F: the letter does not run the action, and Escape is left to the page", () => {
    const run = vi.fn();
    renderHook(() => useNewChord(() => true, run));
    press("KeyC", true);
    press("Escape");
    press("KeyF");
    expect(run).not.toHaveBeenCalled();
  });

  it("Alt+C then F still runs the action", () => {
    const run = vi.fn();
    renderHook(() => useNewChord(() => true, run));
    press("KeyC", true);
    press("KeyF");
    expect(run).toHaveBeenCalledWith("newFolder");
  });
});

describe("useNewChord: a plain key with no chord armed reads no overlay (F7)", () => {
  afterEach(() => {
    document.body.replaceChildren();
    vi.restoreAllMocks();
  });

  it("sends plain keys while an overlay is on the page, and reads no style", () => {
    // An open dialog is on the page, so a read of the overlays would call getComputedStyle for it.
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    document.body.appendChild(dialog);
    const style = vi.spyOn(window, "getComputedStyle");
    const run = vi.fn();
    renderHook(() => useNewChord(() => true, run));
    press("KeyA");
    press("KeyF");
    expect(style).not.toHaveBeenCalled();
    expect(run).not.toHaveBeenCalled();
  });
});
