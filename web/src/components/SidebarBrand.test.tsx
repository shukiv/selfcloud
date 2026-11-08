// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import SidebarBrand from "./SidebarBrand";
import { brand, DEFAULT_ACCENT } from "../lib/brand";

// The brand at the top of the sidebar (decision 109). Expanded: the wordmark, the custom logo, or the name as text. Folded to the
// rail: the mark (the artwork's mark, or the glyph for a custom name), or the custom logo small. Its block is the one the New button
// sits under: a 10 px top edge (6 px on a finger), then the picture, then a 10 px gap (6 px on a finger).
function renderBrand(collapsed: boolean, coarse = false) {
  return render(<SidebarBrand collapsed={collapsed} coarse={coarse} text="#FFFFFF" />);
}

describe("SidebarBrand (decision 109)", () => {
  afterEach(() => brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null }));

  it("shows the wordmark at the top, 38 px high, for the artwork", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: true });
    const { container } = renderBrand(false);
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/wordmark.svg");
    expect(img?.getAttribute("height")).toBe("38");
    expect(img?.getAttribute("alt")).toBe("SelfCloud");
  });

  it("folds to the artwork's mark, not the wordmark, in the rail", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: true });
    const { container } = renderBrand(true);
    expect(container.querySelector("img")).toBeNull();
    expect(container.innerHTML).toContain("/brand/mark.svg");
  });

  it("shows a custom logo in the same place, and small in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: false });
    expect(renderBrand(false).container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
    expect(renderBrand(true).container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
  });

  it("shows a custom name as text, and the glyph in the rail", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const expanded = renderBrand(false).container;
    expect(expanded.textContent).toBe("Acme Drive");
    expect(expanded.querySelector("img")).toBeNull();
    const rail = renderBrand(true).container;
    expect(rail.textContent).toBe("");
    expect(rail.innerHTML).toContain("/brand/mark.svg");
  });

  it("keeps the 10 px edges on a mouse and 6 px on a finger", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: true });
    const mouse = renderBrand(false).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(mouse?.style.paddingBlockStart).toBe("10px");
    expect(mouse?.style.marginBlockEnd).toBe("10px");
    const finger = renderBrand(false, true).container.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(finger?.style.paddingBlockStart).toBe("6px");
    expect(finger?.style.marginBlockEnd).toBe("6px");
  });
});
