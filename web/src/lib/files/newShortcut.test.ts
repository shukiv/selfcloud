// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { CHORD_WINDOW_MS, IDLE, overlayOpen, readChord, chordBlocked, chordNeedsOverlayCheck, type ChordEvent, type ChordState } from "./newShortcut";

// A key press as the browser reports it: key is what the layout typed, code is the key's position.
function press(code: string, extra: Partial<ChordEvent> & { key?: string } = {}): ChordEvent & { key: string } {
  return { key: extra.key ?? "", code, altKey: false, ctrlKey: false, metaKey: false, ...extra };
}
const alt = (code: string, key = "") => press(code, { key, altKey: true });

// step runs one key through the reader; the tests chain the state from one step to the next.
function step(state: ChordState, e: ChordEvent, now = 1000, blocked = false) {
  return readChord(state, e, now, blocked);
}

describe("the New chord reader (Alt+C, then a letter)", () => {
  it("Alt+C arms the chord and is consumed, so the browser does not act on it", () => {
    const s = step(IDLE, alt("KeyC", "c"));
    expect(s.state.armedAt).toBe(1000);
    expect(s.action).toBeNull();
    expect(s.consume).toBe(true);
  });

  it("a plain C, or Alt with another key, does not arm the chord", () => {
    expect(step(IDLE, press("KeyC", { key: "c" })).state.armedAt).toBeNull();
    expect(step(IDLE, alt("KeyX", "x")).state.armedAt).toBeNull();
    expect(step(IDLE, alt("KeyX", "x")).consume).toBe(false);
  });

  it("requires Alt: Ctrl+Alt+C (AltGr on Windows) and Meta+Alt+C do not arm it", () => {
    expect(step(IDLE, press("KeyC", { altKey: true, ctrlKey: true })).state.armedAt).toBeNull();
    expect(step(IDLE, press("KeyC", { altKey: true, metaKey: true })).state.armedAt).toBeNull();
  });

  it.each([
    ["KeyF", "newFolder"],
    ["KeyU", "upload"],
    ["KeyI", "uploadFolder"],
    ["KeyT", "docx"],
    ["KeyS", "xlsx"],
    ["KeyP", "pptx"],
  ])("after Alt+C, %s by its position runs %s, and the key is consumed", (code, action) => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    const s = step(armed, press(code, { key: code.slice(3).toLowerCase() }));
    expect(s.action).toBe(action);
    expect(s.consume).toBe(true);
    expect(s.state).toEqual(IDLE);
  });

  // The layout types Hebrew or Arabic: the key's typed character is not Latin, and the reader must still read the position.
  it("reads the key by its position when the layout types Hebrew (key is not Latin, code is KeyF)", () => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    expect(step(armed, press("KeyF", { key: "כ" })).action).toBe("newFolder");
  });

  it("reads the key by its position when the layout types Arabic (key is not Latin, code is KeyU)", () => {
    const armed = step(IDLE, alt("KeyC", "ç")).state;
    expect(step(armed, press("KeyU", { key: "ي" })).action).toBe("upload");
  });

  it("the chord is armed for 2 seconds: a letter after the window cancels it, and is not consumed", () => {
    const armed = step(IDLE, alt("KeyC", "c"), 1000).state;
    const late = step(armed, press("KeyF", { key: "f" }), 1000 + CHORD_WINDOW_MS + 1);
    expect(late.action).toBeNull();
    expect(late.consume).toBe(false);
    expect(late.state).toEqual(IDLE);
  });

  it("a letter exactly at 2 seconds still counts", () => {
    const armed = step(IDLE, alt("KeyC", "c"), 1000).state;
    expect(step(armed, press("KeyF", { key: "f" }), 1000 + CHORD_WINDOW_MS).action).toBe("newFolder");
  });

  it("a stray key cancels the chord and is not consumed, so the page gets it", () => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    const s = step(armed, press("KeyQ", { key: "q" }));
    expect(s.action).toBeNull();
    expect(s.consume).toBe(false);
    expect(s.state).toEqual(IDLE);
  });

  it("a letter outside the list cancels the chord, with no action", () => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    expect(step(armed, press("KeyZ", { key: "z" })).state).toEqual(IDLE);
  });

  it("Alt+C again while armed re-arms the window and is consumed", () => {
    const armed = step(IDLE, alt("KeyC", "c"), 1000).state;
    const again = step(armed, alt("KeyC", "c"), 1500);
    expect(again.state.armedAt).toBe(1500);
    expect(again.consume).toBe(true);
  });

  it("a key that is only a property name of the object is not an action", () => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    expect(step(armed, press("constructor", { key: "constructor" })).action).toBeNull();
  });

  // macOS: Option+C types ç. The chord is read on keydown, so the key's default is cancelled and no ç is typed.
  it("Option+C on a Mac (the key is ç, the code is KeyC) arms the chord and is consumed", () => {
    const s = step(IDLE, alt("KeyC", "ç"));
    expect(s.state.armedAt).toBe(1000);
    expect(s.consume).toBe(true);
  });

  // A blocked key (a text field, an open menu or dialog) neither arms nor acts, and an armed chord is dropped.
  it("while blocked, Alt+C does nothing and is not consumed", () => {
    const s = step(IDLE, alt("KeyC", "c"), 1000, true);
    expect(s.state.armedAt).toBeNull();
    expect(s.consume).toBe(false);
  });

  it("while blocked, an armed chord is dropped and the letter is not an action", () => {
    const armed = step(IDLE, alt("KeyC", "c")).state;
    const s = step(armed, press("KeyF", { key: "f" }), 1000, true);
    expect(s.action).toBeNull();
    expect(s.consume).toBe(false);
    expect(s.state).toEqual(IDLE);
  });
});

describe("chordBlocked: where the chord is ignored", () => {
  function input(id: string, tag: "input" | "textarea" | "button", type?: string) {
    const el = document.createElement(tag);
    el.id = id;
    if (type && el instanceof HTMLInputElement) el.type = type;
    document.body.appendChild(el);
    return el;
  }

  it("is blocked in a text box and in a text area, and not on a button", () => {
    const text = input("t1", "input", "text");
    const area = input("t2", "textarea");
    const button = input("b1", "button");
    expect(chordBlocked(text)).toBe(true);
    expect(chordBlocked(area)).toBe(true);
    expect(chordBlocked(button)).toBe(false);
    document.body.replaceChildren();
  });

  it("is blocked while a dialog is open, and not after it has closed", () => {
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    document.body.appendChild(dialog);
    expect(overlayOpen(document)).toBe(true);
    expect(chordBlocked(document.body)).toBe(true);
    dialog.style.display = "none";
    expect(overlayOpen(document)).toBe(false);
    document.body.replaceChildren();
  });

  it("is blocked while a menu is open, and not while its dropdown is hidden after closing", () => {
    const hidden = document.createElement("div");
    hidden.className = "ant-dropdown ant-dropdown-hidden";
    const menu = document.createElement("ul");
    menu.setAttribute("role", "menu");
    hidden.appendChild(menu);
    document.body.appendChild(hidden);
    expect(overlayOpen(document)).toBe(false);
    hidden.classList.remove("ant-dropdown-hidden");
    expect(overlayOpen(document)).toBe(true);
    document.body.replaceChildren();
  });

  // The sidebar's navigation is an antd Menu, which is role="menu" and always shown: it is not an open overlay, or the chord
  // would be off on every page that has a sidebar.
  it("is not blocked by the sidebar's navigation menu, which is always shown", () => {
    const nav = document.createElement("ul");
    nav.className = "ant-menu ant-menu-root ant-menu-inline shell-menu";
    nav.setAttribute("role", "menu");
    document.body.appendChild(nav);
    expect(overlayOpen(document)).toBe(false);
    document.body.replaceChildren();
  });

  it("is blocked while a drawer is open", () => {
    const drawer = document.createElement("div");
    drawer.className = "ant-drawer ant-drawer-open";
    document.body.appendChild(drawer);
    expect(overlayOpen(document)).toBe(true);
    document.body.replaceChildren();
  });
});

// The closed drawer and the closed submenu are hidden by the stylesheet, not by an inline style or an antd class on the
// element itself: the test puts the rule in a style element, so the computed display (not the attribute) is what says hidden.
describe("overlayOpen: a stylesheet hides the overlay", () => {
  function withStyle(css: string, build: (root: HTMLElement) => void): () => void {
    const style = document.createElement("style");
    style.textContent = css;
    document.head.appendChild(style);
    const root = document.createElement("div");
    build(root);
    document.body.appendChild(root);
    return () => {
      style.remove();
      document.body.replaceChildren();
    };
  }

  it("a dialog inside a drawer wrapper that only the stylesheet hides (display none) is not open", () => {
    const done = withStyle(".ant-drawer-content-wrapper-hidden { display: none; }", (root) => {
      const wrap = document.createElement("div");
      wrap.className = "ant-drawer-content-wrapper-hidden";
      const dialog = document.createElement("div");
      dialog.setAttribute("role", "dialog");
      wrap.appendChild(dialog);
      root.appendChild(wrap);
    });
    expect(overlayOpen(document)).toBe(false);
    done();
  });

  it("a popup menu inside a submenu that only the stylesheet hides is not open", () => {
    const done = withStyle(".ant-dropdown-menu-submenu-hidden { display: none; }", (root) => {
      const hidden = document.createElement("div");
      hidden.className = "ant-dropdown-menu-submenu-hidden";
      const menu = document.createElement("ul");
      menu.setAttribute("role", "menu");
      hidden.appendChild(menu);
      root.appendChild(hidden);
    });
    expect(overlayOpen(document)).toBe(false);
    done();
  });
});

// The sidebar's own menu is the .shell-menu; another antd menu, shown in a popover or a popup, is an open overlay. A popover
// (Popover and Popconfirm) is open when it is shown and not when its ant-popover-hidden class is set (nt2 review, LOW).
describe("overlayOpen: which menus and popovers are overlays", () => {
  function shownMenu(className: string): HTMLElement {
    const ul = document.createElement("ul");
    ul.className = className;
    ul.setAttribute("role", "menu");
    document.body.appendChild(ul);
    return ul;
  }

  it("the sidebar's own menu (.shell-menu) is not an overlay", () => {
    shownMenu("ant-menu ant-menu-root shell-menu");
    expect(overlayOpen(document)).toBe(false);
    document.body.replaceChildren();
  });

  it("another antd menu that is shown is an overlay, even when it has the ant-menu class", () => {
    shownMenu("ant-menu ant-menu-root ant-menu-vertical");
    expect(overlayOpen(document)).toBe(true);
    document.body.replaceChildren();
  });

  it("a shown popover is an overlay, and a popover with ant-popover-hidden is not", () => {
    const pop = document.createElement("div");
    pop.className = "ant-popover ant-popover-placement-bottom";
    document.body.appendChild(pop);
    expect(overlayOpen(document)).toBe(true);
    pop.classList.add("ant-popover-hidden");
    expect(overlayOpen(document)).toBe(false);
    document.body.replaceChildren();
  });
});

describe("the chord's text fields and its overlay check (F6, F7)", () => {
  it("a contenteditable element blocks the chord, and so does a node inside it", () => {
    const editable = document.createElement("div");
    editable.setAttribute("contenteditable", "true");
    const inner = document.createElement("span");
    editable.appendChild(inner);
    document.body.appendChild(editable);
    expect(chordBlocked(editable)).toBe(true);
    expect(chordBlocked(inner)).toBe(true);
    document.body.replaceChildren();
  });

  it("looks at the overlays only for a key that could act: Alt+C, or a key while the chord is armed", () => {
    expect(chordNeedsOverlayCheck(IDLE, press("KeyF"), 1000)).toBe(false);
    expect(chordNeedsOverlayCheck(IDLE, press("KeyA"), 1000)).toBe(false);
    expect(chordNeedsOverlayCheck(IDLE, alt("KeyC", "c"), 1000)).toBe(true);
    expect(chordNeedsOverlayCheck({ armedAt: 1000 }, press("KeyF"), 1500)).toBe(true);
    expect(chordNeedsOverlayCheck({ armedAt: 1000 }, press("KeyF"), 1000 + CHORD_WINDOW_MS + 1)).toBe(false);
  });
});
