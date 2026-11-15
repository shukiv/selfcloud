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
import { RADIUS, SIDEBAR } from "../../theme/tokens";
import { sidebarRow } from "../../theme/sidebarRow";
import { contrast } from "../../lib/brand";
import SidebarStorage, { StorageRail, storageTitle } from "./SidebarStorage";
import { formatBytes } from "../../lib/files/format";

// The rows' inline margin (decision 185): the block takes it too, so its edges are the rows' edges.
const ROW_INLINE = sidebarRow(antdTheme.getDesignToken(), false).inline;
// The menu rows' inline padding (antd's Menu itemPaddingInline is token.padding, decision 185): the block's content ends this far
// short of its end edge, so the bar, the title and the figures do not run into the block's end edge and its hover/active fill.
const ROW_PAD_END = antdTheme.getDesignToken().padding;

// The stylesheet's text, read from the web root that vitest runs in: the vitest run does not load CSS, and a ?raw import is empty.
const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const s = (used: number, reserved: number, quota: number | null): MeStorage => ({ used_bytes: used, reserved_bytes: reserved, quota_bytes: quota, available_bytes: null });

// The block is one router link, so it is drawn inside a router, as the shell draws it.
const draw = (ui: ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

// rule returns the text of the first CSS rule whose selector starts with the given text.
function rule(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`no rule for ${selector}`);
  return css.slice(start, css.indexOf("}", start) + 1);
}

// The storage block at the foot of the sidebar (decision 181's pin, decision 185's look): no card frame, a sidebar row's edges,
// hover and radius. Line 1 is a Cloud icon in the rows' icon column and the storage title in the rows' label column; line 2 the
// bar, only with a limit; line 3 the figures in the caption colour. On the Storage page it takes the selected menu row's look:
// the active fill and the on-active ink, and the bar's normal fill takes the on-active ink too, so it keeps 3:1 against its track.
describe("the sidebar's storage block (decisions 181 and 185)", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("with a limit: the Cloud icon and the title, a 4 px bar in the accent colour, and the figures line", () => {
    draw(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const title = screen.getByText("Storage (61% full)");
    expect(title.style.fontSize).toBe("14px");
    expect(title.className).toBe("shell-storage-title");
    const icon = document.querySelector<SVGElement>(".shell-storage-card svg");
    expect(icon?.getAttribute("class")).toContain("lucide-cloud");
    expect(icon?.getAttribute("width")).toBe("18");
    expect(icon?.getAttribute("height")).toBe("18");
    expect(icon?.getAttribute("stroke-width")).toBe("1.75");
    expect(icon?.getAttribute("aria-hidden")).toBe("true");
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.dataset.level).toBe("ok");
    expect(bar?.style.blockSize).toBe("4px");
    expect(bar?.style.borderRadius).toBe("2px");
    expect(bar?.style.background).toBe("rgba(0, 0, 0, 0.35)");
    expect(bar?.style.boxShadow).toBe(`inset 0 0 0 1px ${SIDEBAR.caption}`);
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe("rgb(254, 204, 0)");
    const line = screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`);
    expect(line.style.fontSize).toBe("12px");
    expect(line.style.color).toBe("rgb(169, 193, 230)");
  });

  it("with no limit: the title and the used line, no bar and no percent", () => {
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

  it("shows the title alone while the figures are not read yet", () => {
    draw(<SidebarStorage my={null} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(document.querySelector('[data-testid="sidebar-meter"]')).toBeNull();
    expect(screen.queryByText(/used/)).toBeNull();
  });

  it("is one link to /storage, named by the title and the figures line", () => {
    draw(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const link = screen.getByRole("link", { name: `Storage (61% full) ${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used` });
    expect(link.getAttribute("href")).toBe("/storage");
    // The bar and the icon are decorative: they are not part of the name.
    expect(link.querySelector('[data-testid="sidebar-meter"]')?.getAttribute("aria-hidden")).toBe("true");
    expect(link.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("with no limit the link's name is the title and the used line", () => {
    draw(<SidebarStorage my={s(153 * MB, 0, null)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    expect(screen.getByRole("link", { name: `Storage ${formatBytes(153 * MB, "en")} used` })).toBeTruthy();
  });

  // Decision 185: no card frame. The block is a sidebar row: the rows' radius, the rows' hover tint and the same focus ring, with
  // no border and no background of its own. Its content starts in the rows' icon column, the title an 18 px icon and a 10 px gap
  // in (the menu's own icon and gap), and ends one menu-row inline padding (token.padding) short of the block's end edge.
  it("is a sidebar row, not a card: no border, no background, the rows' radius, hover and focus ring", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.style.border).toBe("");
    expect(card.style.borderRadius).toBe(`${RADIUS}px`);
    expect(card.style.background).toBe("");
    expect(card.style.getPropertyValue("--sc-card-bg")).toBe("");
    expect(card.style.getPropertyValue("--sc-card-hover")).toBe("");
    expect(card.style.paddingInlineStart).toBe("var(--sc-icon-col, 24px)");
    // The end padding is the menu rows' own inline padding, read from the antd token (token.padding), not a literal.
    expect(card.style.paddingInlineEnd).toBe(`${ROW_PAD_END}px`);
    const titleRow = card.querySelector<HTMLElement>(".shell-storage-title")?.parentElement;
    expect(titleRow?.style.display).toBe("flex");
    expect(titleRow?.style.gap).toBe("10px");
    expect(rule(indexCss, ".shell-storage-card:hover")).toContain("var(--sc-side-hover");
    expect(rule(indexCss, ".shell-storage-card:focus-visible")).toContain("outline");
  });

  // Decision 185: the block takes the rows' inline margin, so it starts and ends where the menu's rows and the tree's rows do.
  it("takes the rows' inline margin, so its edges are the rows' edges (decision 185)", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.style.marginInline).toBe(`${ROW_INLINE}px`);
  });

  // Decision 185: on the Storage page the block takes the selected menu row's look: the stylesheet fills it with the active colour
  // (on hover too), and the title, the figures and the bar's outline take the on-active ink. The bar's normal fill takes it too, so
  // it keeps 3:1 against its track on the fill (the warning and error colours keep themselves).
  it("on the Storage page it is the current place: the selected row's fill and ink, the bar's fill in the on-active ink", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} selected />);
    const card = screen.getByTestId("storage-card");
    expect(card.getAttribute("aria-current")).toBe("page");
    const fill = rule(indexCss, '.shell-storage-card[aria-current="page"]');
    expect(fill).toContain("var(--sc-side-active");
    expect(rule(indexCss, '.shell-storage-card[aria-current="page"]:hover')).toContain("var(--sc-side-active");
    const onActive = `rgb(${parseInt(SIDEBAR.onActive.slice(1, 3), 16)}, ${parseInt(SIDEBAR.onActive.slice(3, 5), 16)}, ${parseInt(SIDEBAR.onActive.slice(5, 7), 16)})`;
    expect(screen.getByText("Storage (61% full)").style.color).toBe("");
    expect(card.style.color).toBe(onActive);
    const line = screen.getByText(`${formatBytes(610, "en")} of ${formatBytes(1000, "en")} used`);
    expect(line.style.color).toBe(onActive);
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.style.background).toBe("rgba(0, 0, 0, 0.35)");
    expect(bar?.style.boxShadow).toBe(`inset 0 0 0 1px ${SIDEBAR.onActive}`);
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe(onActive);
    // The on-active ink reads on the active fill, as the selected menu row's text does.
    expect(contrast(SIDEBAR.onActive, SIDEBAR.active)).toBeGreaterThanOrEqual(4.5);
  });

  it("off the Storage page it is not current: no aria-current, the title in the sidebar's text and the figures in the caption", () => {
    draw(<SidebarStorage my={s(610, 0, 1000)} sidebar={SIDEBAR} inline={ROW_INLINE} />);
    const card = screen.getByTestId("storage-card");
    expect(card.getAttribute("aria-current")).toBeNull();
    expect(card.style.color).toBe("rgb(255, 255, 255)");
    expect(screen.getByText(`${formatBytes(610, "en")} of ${formatBytes(1000, "en")} used`).style.color).toBe("rgb(169, 193, 230)");
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

  it("is one Cloud icon link to /storage, named by the storage title, with that tooltip", async () => {
    draw(<StorageRail my={s(610, 0, 1000)} sidebar={SIDEBAR} />);
    const link = screen.getByTestId("storage-rail");
    expect(link.getAttribute("href")).toBe("/storage");
    expect(link.getAttribute("aria-label")).toBe("Storage (61% full)");
    // The same Cloud shape as the expanded block (decision 185).
    const icon = link.querySelector("svg");
    expect(icon?.getAttribute("class")).toContain("lucide-cloud");
    expect(icon?.getAttribute("aria-hidden")).toBe("true");
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
