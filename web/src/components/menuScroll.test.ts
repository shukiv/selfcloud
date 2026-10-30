// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { keepInView } from "./menuScroll";

// box is a scrolling menu whose visible part is 100 to 300 on the page; item is drawn at itemTop.
function fakes(itemTop: number, itemHeight = 40, scrollTop = 0) {
  const box = document.createElement("div");
  const item = document.createElement("div");
  // Only the vertical edges matter here; the rest of the rectangle is left out.
  box.getBoundingClientRect = () => ({ top: 100, bottom: 300, height: 200, x: 0, y: 100, toJSON() {} }) as DOMRect;
  item.getBoundingClientRect = () => ({ top: itemTop, bottom: itemTop + itemHeight, height: itemHeight, x: 0, y: itemTop, toJSON() {} }) as DOMRect;
  box.scrollTop = scrollTop;
  return { box, item };
}

describe("keepInView", () => {
  // The selected item sits below the fold: the menu scrolls, so the item is whole inside it (guard: the scrollTop move).
  it("scrolls the menu, not the page, so a selected item below the fold is whole", () => {
    const { box, item } = fakes(340);
    const pageScroll = vi.spyOn(window, "scrollTo");
    keepInView(box, item);
    expect(box.scrollTop).toBe(80);
    expect(pageScroll).not.toHaveBeenCalled();
  });

  it("scrolls back up to an item above the top of the menu", () => {
    const { box, item } = fakes(60, 40, 120);
    keepInView(box, item);
    expect(box.scrollTop).toBe(80);
  });

  it("leaves the menu alone when the item is already visible", () => {
    const { box, item } = fakes(150, 40, 10);
    keepInView(box, item);
    expect(box.scrollTop).toBe(10);
  });
});
