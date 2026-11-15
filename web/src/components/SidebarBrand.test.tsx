// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { theme as antdTheme } from "antd";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import SidebarBrand from "./SidebarBrand";
import { brand, brandLoaded, DEFAULT_ACCENT } from "../lib/brand";
import { sidebarRow } from "../theme/sidebarRow";

// The brand at the top of the sidebar (decision 109, its edge replaced by decision 193). Expanded: the wordmark (or the tile
// logo where the wordmark would not read) fills the block's width, the custom logo is centred at up to 42 px high, or the name
// as text. The block takes the rows' inline margin, so every part of the sidebar starts at one edge, and the space above the
// picture and below the block is S, the sider's inline padding plus that margin, on a mouse and a finger alike. Folded to the
// rail: the SelfCloud mark for every name (decision 119), or the custom logo small, with the rail's own edges unchanged.
const SIDEBAR_BLUE = "#053D8E";
// The desktop sider's inline padding and width (Shell.tsx). The block's content width is the width less S at each side.
const SIDE_PAD = 12;
const SIDE_WIDTH = 264;
function metrics(coarse: boolean) {
  const row = sidebarRow(antdTheme.getDesignToken(), coarse);
  return { row, edge: SIDE_PAD + row.inline };
}
function renderBrand(collapsed: boolean, coarse = false, text = "#FFFFFF", background = SIDEBAR_BLUE, width = SIDE_WIDTH) {
  const { row, edge } = metrics(coarse);
  return render(<SidebarBrand collapsed={collapsed} coarse={coarse} text={text} background={background} inline={row.inline} edge={edge} width={width} />);
}
// reserved is the height the picture the background draws will have at the block's content width, rounded up, so the box keeps its
// height when the brand loads (decision 193): the wordmark (38/211) on a dark sidebar, the taller tile logo (398/1731.65) on a light one.
const reserved = (width: number, edge: number, light = false) => Math.ceil((width - 2 * edge) * (light ? 398 / 1731.65 : 38 / 211));

describe("SidebarBrand (decisions 109 and 193)", () => {
  beforeEach(() => brandLoaded.set(true));
  afterEach(() => {
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
  });

  it("shows the wordmark filling the block's width, for the artwork on the default blue", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderBrand(false);
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/wordmark.svg");
    expect(img?.getAttribute("alt")).toBe("SelfCloud");
    expect(img?.style.width).toBe("100%");
    expect(img?.style.height).toBe("auto");
    expect(img?.style.display).toBe("block");
  });

  // The wordmark's letters are white, so on a light sidebar the tile logo is drawn instead (decision 109), filling the width too.
  it("shows the tile logo, not the wordmark, when a light accent makes the sidebar too light for white", () => {
    brand.set({ name: "SelfCloud", accent: "#FFF9C4", logo_url: null, artwork: true });
    const { container } = renderBrand(false, false, "#0D2142", "#FFF9C4");
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/logo");
    expect(img?.style.width).toBe("100%");
    expect(img?.style.height).toBe("auto");
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

  // Decision 193: an uploaded logo keeps its aspect, centred in the block, at most as wide as the block and 42 px high.
  it("shows a custom logo centred, at most 42 px high and as wide as the block, and small in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: false });
    const expanded = renderBrand(false).container;
    const img = expanded.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/logo");
    expect(img?.style.maxWidth).toBe("100%");
    expect(img?.style.maxHeight).toBe("42px");
    expect(img?.style.objectFit).toBe("contain");
    expect(img?.style.width).toBe("auto");
    expect(img?.style.height).toBe("auto");
    expect(expanded.querySelector<HTMLElement>('[data-testid="sidebar-brand"]')?.style.justifyContent).toBe("center");
    const rail = renderBrand(true).container.querySelector("img");
    expect(rail?.getAttribute("src")).toBe("/brand/logo");
    expect(rail?.getAttribute("height")).toBe("28");
    expect(rail?.style.maxWidth).toBe("40px");
  });

  // A logo URL goes through safeLogo before it reaches an image: an unsafe one shows no picture at all.
  it("shows no picture for an unsafe logo URL", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "javascript:alert(1)", artwork: false });
    const { container } = renderBrand(false);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("Acme Drive");
  });

  it("shows a custom name as text at the start edge, and the mark in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const expanded = renderBrand(false).container;
    expect(expanded.textContent).toBe("Acme Drive");
    expect(expanded.querySelector("img")).toBeNull();
    expect(expanded.querySelector<HTMLElement>('[data-testid="sidebar-brand"]')?.style.justifyContent).toBe("flex-start");
    const rail = renderBrand(true).container;
    expect(rail.textContent).toBe("");
    expect(rail.querySelector('[role="img"]')?.getAttribute("style")).toContain("/brand/mark.svg");
  });

  // Before the server's answer nothing is drawn, and the block keeps the wordmark's height so nothing moves when the brand arrives.
  it("draws no name and no picture before the brand is loaded, and keeps the space", () => {
    brandLoaded.set(false);
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const { container } = renderBrand(false);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");
    const block = container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    const { edge } = metrics(false);
    expect(block?.style.minHeight).toBe(`${reserved(SIDE_WIDTH, edge)}px`);
    expect(block?.style.marginBlockEnd).toBe(`${edge}px`);
  });

  // The reserved space is the height the loaded brand takes, so the sidebar does not move when the info arrives (the w2 e2e drag
  // depends on it: a move of 58 px shifted the rows under a press). Decision 193: the wordmark fills the width, so the height is
  // the content width x 38/211 rounded up, 42 px at the desktop's 232 px content, with S (16 px) above and below.
  it("reserves the height the loaded brand takes, so nothing moves when it arrives", () => {
    const box = (el: HTMLElement | null) => [el?.style.minHeight, el?.style.paddingBlockStart, el?.style.marginBlockEnd];
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const reservedBox = box(renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'));
    brandLoaded.set(true);
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const loaded = box(renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'));
    expect(reservedBox).toEqual(["42px", "16px", "16px"]);
    expect(loaded).toEqual(reservedBox);
  });

  // Decision 193: on a light sidebar the picture is the tile logo (/brand/logo, internal/web/brand/selfcloud-logo.svg, viewBox
  // 1731.65 x 398), taller than the wordmark at the same width. The reserved height is the tile's own, so the box does not grow when
  // the brand loads (the must-never-happen). The dark case above keeps the wordmark's 42 px.
  it("reserves the tile logo's height on a light sidebar, so the box does not grow when it loads", () => {
    const box = (el: HTMLElement | null) => [el?.style.minHeight, el?.style.paddingBlockStart, el?.style.marginBlockEnd];
    const { edge } = metrics(false);
    const light = "#FFF9C4";
    brandLoaded.set(false);
    brand.set({ name: "", accent: light, logo_url: null, artwork: true });
    const reservedBox = box(renderBrand(false, false, "#0D2142", light).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'));
    brandLoaded.set(true);
    brand.set({ name: "SelfCloud", accent: light, logo_url: null, artwork: true });
    const loaded = renderBrand(false, false, "#0D2142", light).container;
    expect(loaded.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
    expect(box(loaded.querySelector<HTMLElement>('[data-testid="sidebar-brand"]'))).toEqual(reservedBox);
    // The tile picture's height at the 232 px content width: ceil(232 x 398/1731.65) = 54 px, not the wordmark's 42.
    expect(reservedBox[0]).toBe(`${reserved(SIDE_WIDTH, edge, true)}px`);
    expect(reservedBox[0]).toBe("54px");
  });

  // Decision 193 replaces decision 109's 10 px (6 px on a finger) edge: the block's top and bottom space is S on a mouse and a
  // finger alike, and its inline margin is the rows' own, so the brand starts where the rows start.
  it("keeps the side space S above and below, and the rows' inline margin, on a mouse and a finger alike", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    for (const coarse of [false, true]) {
      const { row, edge } = metrics(coarse);
      const { container, unmount } = renderBrand(false, coarse);
      const block = container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
      expect(block?.style.paddingBlockStart).toBe(`${edge}px`);
      expect(block?.style.marginBlockEnd).toBe(`${edge}px`);
      expect(block?.style.marginInline).toBe(`${row.inline}px`);
      unmount();
    }
  });

  // The phone drawer is wider than the sider (280 px), so its reserved wordmark height is read at that width.
  it("reserves the wordmark's height at the drawer's width too", () => {
    brandLoaded.set(false);
    const { container } = renderBrand(false, false, "#FFFFFF", SIDEBAR_BLUE, 280);
    const block = container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    const { edge } = metrics(false);
    expect(block?.style.minHeight).toBe(`${reserved(280, edge)}px`);
  });

  // Decision 193 leaves the collapsed rail unchanged: its block keeps the old edges, its 32 px mark box and no inline margin.
  it("the folded rail's brand block keeps its own styles, on a mouse and a finger", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const mouse = renderBrand(true).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(mouse?.style.cssText).toMatchInlineSnapshot(`"flex: 0 0 auto; display: flex; align-items: center; justify-content: center; min-width: 0px; min-height: 32px; padding-block-start: 10px; margin-block-end: 10px;"`);
    const finger = renderBrand(true, true).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(finger?.style.cssText).toMatchInlineSnapshot(`"flex: 0 0 auto; display: flex; align-items: center; justify-content: center; min-width: 0px; min-height: 32px; padding-block-start: 6px; margin-block-end: 6px;"`);
  });
});