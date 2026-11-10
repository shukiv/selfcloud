// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import SidebarBrand from "./SidebarBrand";
import { brand, brandLoaded, DEFAULT_ACCENT } from "../lib/brand";

// The brand at the top of the sidebar (decision 109). Expanded: the wordmark, the custom logo, or the name as text. Folded to the
// rail: the SelfCloud mark for every name (decision 119), or the custom logo small. Its block is the one the New button
// sits under: a 10 px top edge (6 px on a finger), then the picture, then a 10 px gap (6 px on a finger).
const SIDEBAR_BLUE = "#053D8E";
function renderBrand(collapsed: boolean, coarse = false, text = "#FFFFFF", background = SIDEBAR_BLUE) {
  return render(<SidebarBrand collapsed={collapsed} coarse={coarse} text={text} background={background} />);
}

describe("SidebarBrand (decision 109)", () => {
  beforeEach(() => brandLoaded.set(true));
  afterEach(() => {
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
  });

  it("shows the wordmark at the top, 38 px high, for the artwork on the default blue", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderBrand(false);
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/wordmark.svg");
    expect(img?.getAttribute("height")).toBe("38");
    expect(img?.getAttribute("alt")).toBe("SelfCloud");
  });

  // The wordmark's letters are white, so on a light sidebar the tile logo is drawn instead (decision 109).
  it("shows the tile logo, not the wordmark, when a light accent makes the sidebar too light for white", () => {
    brand.set({ name: "SelfCloud", accent: "#FFF9C4", logo_url: null, artwork: true });
    const { container } = renderBrand(false, false, "#0D2142", "#FFF9C4");
    expect(container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
  });

  // The rail's mark takes the sidebar's ink, which is chosen against the sidebar: dark on a light brand, white on the blue.
  it("draws the rail's mark in the ink the sidebar chose, so it reads on a light brand", () => {
    brand.set({ name: "SelfCloud", accent: "#FFF9C4", logo_url: null, artwork: true });
    const { container } = renderBrand(true, false, "#0D2142", "#FFF9C4");
    const mark = container.querySelector<HTMLElement>('[role="img"]');
    expect(mark?.style.background).toBe("rgb(13, 33, 66)");
  });

  it("folds to the artwork's mark, not the wordmark, in the rail", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderBrand(true);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector('[role="img"]')?.getAttribute("style")).toContain("/brand/mark.svg");
  });

  // Decision 126: a server with no logo file sends the artwork whatever the name, so a custom name gets the wordmark, labelled
  // with that name, and no text.
  it("shows the wordmark for a custom name with the artwork, labelled with the name", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderBrand(false);
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/wordmark.svg");
    expect(img?.getAttribute("alt")).toBe("Acme Drive");
    expect(container.textContent).toBe("");
  });

  it("shows a custom logo in the same place, and small in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: false });
    expect(renderBrand(false).container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
    expect(renderBrand(true).container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
  });

  // A logo URL goes through safeLogo before it reaches an image: an unsafe one shows no picture at all.
  it("shows no picture for an unsafe logo URL", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "javascript:alert(1)", artwork: false });
    const { container } = renderBrand(false);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("Acme Drive");
  });

  it("shows a custom name as text, and the mark in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const expanded = renderBrand(false).container;
    expect(expanded.textContent).toBe("Acme Drive");
    expect(expanded.querySelector("img")).toBeNull();
    const rail = renderBrand(true).container;
    expect(rail.textContent).toBe("");
    expect(rail.querySelector('[role="img"]')?.getAttribute("style")).toContain("/brand/mark.svg");
  });

  // Before the server's answer nothing is drawn, and the block keeps its height so nothing moves when the brand arrives.
  it("draws no name and no picture before the brand is loaded, and keeps the space", () => {
    brandLoaded.set(false);
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const { container } = renderBrand(false);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");
    const block = container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(block?.style.minHeight).toBe("38px");
    expect(block?.style.marginBlockEnd).toBe("10px");
  });

  // The reserved space is the height the loaded brand takes, so the sidebar does not move when the info arrives (the w2 e2e drag
  // depends on it: a move of 58 px shifted the rows under a press).
  it("reserves the height the loaded brand takes, so nothing moves when it arrives", () => {
    const box = (el: HTMLElement | null) => [el?.style.minHeight, el?.style.paddingBlockStart, el?.style.marginBlockEnd];
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const reserved = box(renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'));
    brandLoaded.set(true);
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const loaded = box(renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'));
    expect(reserved).toEqual(["38px", "10px", "10px"]);
    expect(loaded).toEqual(reserved);
  });

  it("keeps the 10 px edges on a mouse and 6 px on a finger", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const mouse = renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(mouse?.style.paddingBlockStart).toBe("10px");
    expect(mouse?.style.marginBlockEnd).toBe("10px");
    const finger = renderBrand(false, true).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(finger?.style.paddingBlockStart).toBe("6px");
    expect(finger?.style.marginBlockEnd).toBe("6px");
  });
});
