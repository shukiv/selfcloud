// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { ReactNode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { theme as antdTheme } from "antd";
import { setLang } from "../../lib/i18n";
import type { MeStorage } from "../../lib/storage/logic";
import { SIDEBAR } from "../../theme/tokens";
import { currentMark } from "../../theme/currentMark";
import { sidebarRow } from "../../theme/sidebarRow";
import SidebarStorage, { StorageRail, storageTitle } from "./SidebarStorage";
import { formatBytes } from "../../lib/files/format";

// The rows' inline margin (decision 185): the card takes it too, so its edges are the rows' edges.
const ROW_INLINE = sidebarRow(antdTheme.getDesignToken(), false).inline;

// The stylesheet's text, read from the web root that vitest runs in: the vitest run does not load CSS, and a ?raw import is empty.
const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const s = (used: number, reserved: number, quota: number | null): MeStorage => ({ used_bytes: used, reserved_bytes: reserved, quota_bytes: quota, available_bytes: null });

// The card is one router link, so it is drawn inside a router, as the shell draws it.
const draw = (ui: ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

// rule returns the text of the first CSS rule whose selector starts with the given text.
function rule(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`no rule for ${selector}`);
  return css.slice(start, css.indexOf("}", start) + 1);
}

describe("the sidebar's storage card (decision 181)", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("with a limit: the caption, a 6 px bar in the accent colour, and the figures line", () => {
    draw(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const caption = screen.getByText("Storage");
    expect(caption.style.fontSize).toBe("12px");
    expect(caption.style.color).toBe("rgb(169, 193, 230)");
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.dataset.level).toBe("ok");
    expect(bar?.style.blockSize).toBe("6px");
    expect(bar?.style.background).toBe("rgba(0, 0, 0, 0.35)");
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe("rgb(254, 204, 0)");
    const line = screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`);
    expect(line.style.fontSize).toBe("12px");
    expect(line.style.color).toBe("rgb(255, 255, 255)");
  });

  it("with no limit: the caption and the used line, no bar and no percent", () => {
    draw(<SidebarStorage my={s(153 * MB, 0, null)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(screen.getByText(`${formatBytes(153 * MB, "en")} used`)).toBeTruthy();
    expect(document.querySelector('[data-testid="sidebar-meter"]')).toBeNull();
    expect(screen.queryByText(/%/)).toBeNull();
  });

  it("counts the bytes reserved for uploads, as the Storage page does: the bar is at 61%", () => {
    draw(<SidebarStorage my={s(GB * 1.2, GB * 0.02, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.inlineSize).toBe("61%");
  });

  it("draws the bar at the same figure as the line: 99.6% reads 99% and the bar is not full", () => {
    draw(<SidebarStorage my={s(996, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.inlineSize).toBe("99%");
    expect(screen.getByText(`${formatBytes(996, "en")} of ${formatBytes(1000, "en")} used`)).toBeTruthy();
  });

  it("is the warning colour from 80%, and the error colour at 100% and over", () => {
    const { unmount } = draw(<SidebarStorage my={s(850, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const warning = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(warning?.getAttribute("data-level")).toBe("warning");
    expect(warning?.firstElementChild instanceof HTMLElement && warning.firstElementChild.style.background).toBe("rgb(255, 169, 64)");
    unmount();
    const over = draw(<SidebarStorage my={s(1000, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const danger = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(danger?.getAttribute("data-level")).toBe("danger");
    expect(danger?.firstElementChild instanceof HTMLElement && danger.firstElementChild.style.background).toBe("rgb(255, 107, 107)");
    over.unmount();
  });

  it("shows the caption alone while the figures are not read yet", () => {
    draw(<SidebarStorage my={null} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(document.querySelector('[data-testid="sidebar-meter"]')).toBeNull();
    expect(screen.queryByText(/used/)).toBeNull();
  });

  it("is one link to /storage, named by the caption and the figures line", () => {
    draw(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const link = screen.getByRole("link", { name: `Storage ${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used` });
    expect(link.getAttribute("href")).toBe("/storage");
    // The bar is decorative: it is not part of the name.
    expect(link.querySelector('[data-testid="sidebar-meter"]')?.getAttribute("aria-hidden")).toBe("true");
  });

  it("with no limit the link's name is the caption and the used line", () => {
    draw(<SidebarStorage my={s(153 * MB, 0, null)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    expect(screen.getByRole("link", { name: `Storage ${formatBytes(153 * MB, "en")} used` })).toBeTruthy();
  });

  it("is the owner's card: radius 4, padding 12, the card background and its 1 px border", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.style.borderRadius).toBe("4px");
    expect(card.style.padding).toBe("12px");
    expect(card.style.getPropertyValue("--sc-card-bg")).toBe(SIDEBAR.card);
    expect(card.style.border).toBe(`1px solid ${SIDEBAR.cardBorder}`);
    // Hover lightens the card a little: the hover tint is the card's white with more of it.
    expect(card.style.getPropertyValue("--sc-card-hover")).toBe("rgba(255, 255, 255, 0.14)");
    expect(rule(indexCss, ".shell-storage-card:hover")).toContain("var(--sc-card-hover)");
    expect(rule(indexCss, ".shell-storage-card:focus-visible")).toContain("outline");
  });

  // Decision 185: the card takes the rows' inline margin, so it starts and ends where the menu's rows and the tree's rows do.
  it("takes the rows' inline margin, so its edges are the rows' edges (decision 185)", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.style.marginInline).toBe(`${ROW_INLINE}px`);
  });

  it("on the Storage page it is the current place: aria-current page and the border in the mark colour", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} selected />);
    const card = screen.getByTestId("storage-card");
    expect(card.getAttribute("aria-current")).toBe("page");
    // The border takes the sidebar's mark colour (currentMark), the yellow on the default blue.
    const mark = currentMark(SIDEBAR).bar;
    const rgb = `rgb(${parseInt(mark.slice(1, 3), 16)}, ${parseInt(mark.slice(3, 5), 16)}, ${parseInt(mark.slice(5, 7), 16)})`;
    expect(card.style.borderColor).toBe(rgb);
  });

  it("off the Storage page it is not current: no aria-current, the card's own border", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.getAttribute("aria-current")).toBeNull();
    expect(card.style.border).toBe(`1px solid ${SIDEBAR.cardBorder}`);
  });

  it("the figures keep their colours on the Storage page: the mark is the border, not a fill", () => {
    draw(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} selected />);
    expect(screen.getByText("Storage").style.color).toBe("rgb(169, 193, 230)");
    const line = screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`);
    expect(line.style.color).toBe("rgb(255, 255, 255)");
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.style.background).toBe("rgba(0, 0, 0, 0.35)");
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe("rgb(254, 204, 0)");
  });

  it("gives the collapsed rail the title alone, for its tooltip", () => {
    const t = (key: string, params?: Record<string, string>) => (key === "nav.storageFull" ? `Storage (${params?.percent}% full)` : "Storage");
    expect(storageTitle(s(610, 0, 1000), t)).toBe("Storage (61% full)");
    expect(storageTitle(s(610, 0, null), t)).toBe("Storage");
    expect(storageTitle(null, t)).toBe("Storage");
  });
});

describe("the folded rail's storage icon link (decision 181)", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("is one icon link to /storage, named by the storage title, with that tooltip", async () => {
    draw(<StorageRail my={s(610, 0, 1000)} sidebar={SIDEBAR} />);
    const link = screen.getByTestId("storage-rail");
    expect(link.getAttribute("href")).toBe("/storage");
    expect(link.getAttribute("aria-label")).toBe("Storage (61% full)");
    expect(link.querySelector("svg")).not.toBeNull();
    expect(link.textContent).toBe("");
    fireEvent.mouseEnter(link);
    const tip = await waitFor(() => {
      const el = document.querySelector('[role="tooltip"]');
      if (!el?.textContent) throw new Error("no tooltip yet");
      return el.textContent;
    });
    expect(tip).toBe("Storage (61% full)");
  });

  it("with no limit, and before the figures are read, its name is Storage", () => {
    const { unmount } = draw(<StorageRail my={s(610, 0, null)} sidebar={SIDEBAR} />);
    expect(screen.getByTestId("storage-rail").getAttribute("aria-label")).toBe("Storage");
    unmount();
    draw(<StorageRail my={null} sidebar={SIDEBAR} />);
    expect(screen.getByTestId("storage-rail").getAttribute("aria-label")).toBe("Storage");
  });

  it("on the Storage page it is the current place: aria-current page, filled with the active colour as the rail's rows are", () => {
    draw(<StorageRail my={s(610, 0, 1000)} sidebar={SIDEBAR} selected />);
    const link = screen.getByTestId("storage-rail");
    expect(link.getAttribute("aria-current")).toBe("page");
    // The rail's colours come from the stylesheet's variables (the shell sets them), as the rail's menu rows do.
    const current = rule(indexCss, '.shell-storage-rail[aria-current="page"]');
    expect(current).toContain("var(--sc-side-active");
    expect(current).toContain("var(--sc-side-on-active");
    expect(rule(indexCss, ".shell-storage-rail:focus-visible")).toContain("outline");
  });

  it("off the Storage page it is not current", () => {
    draw(<StorageRail my={s(610, 0, 1000)} sidebar={SIDEBAR} />);
    expect(screen.getByTestId("storage-rail").getAttribute("aria-current")).toBeNull();
  });
});
