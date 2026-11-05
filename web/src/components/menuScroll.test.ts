// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { followTarget, keepInView } from "./menuScroll";

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

// The sidebar's region as the shell draws it: the folder tree, then the menu. The tree marks the current place on its treeitem,
// which also holds the open folders under it.
function region(current: "team" | "menu" | "none") {
  const box = document.createElement("div");
  box.innerHTML = `
    <ul role="tree">
      <li role="treeitem" ${current === "team" ? 'aria-current="location"' : ""}>
        <div class="tree-row">Team A</div>
        <ul role="group"><li role="treeitem"><div class="tree-row">D1 Projects</div></li></ul>
      </li>
    </ul>
    <ul class="shell-menu"><li class="ant-menu-item ${current === "menu" ? "ant-menu-item-selected" : ""}">Starred</li></ul>`;
  return box;
}

describe("followTarget", () => {
  // Gate stfu4r1: keeping the whole treeitem in view scrolled to the bottom of Team A's open folders, so Team A's own row and the
  // folders under it went off the top of the sidebar (decision 81).
  it("is the current place's own row, not the open folders under it", () => {
    const box = region("team");
    expect(followTarget(box)).toBe(box.querySelector('[aria-current="location"] > .tree-row'));
  });

  it("is the menu's selected item itself", () => {
    const box = region("menu");
    expect(followTarget(box)).toBe(box.querySelector(".ant-menu-item-selected"));
  });

  it("is nothing when no place is current", () => {
    expect(followTarget(region("none"))).toBeNull();
  });
});
