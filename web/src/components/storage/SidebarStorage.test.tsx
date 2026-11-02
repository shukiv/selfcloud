// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { setLang } from "../../lib/i18n";
import type { MeStorage } from "../../lib/storage/logic";
import { SIDEBAR } from "../../theme/tokens";
import SidebarStorage, { storageTitle } from "./SidebarStorage";
import { formatBytes } from "../../lib/files/format";

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const s = (used: number, reserved: number, quota: number | null): MeStorage => ({ used_bytes: used, reserved_bytes: reserved, quota_bytes: quota, available_bytes: null });

describe("the sidebar's storage block", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("has the row's title with no limit, and the line says what is used, with no bar and no percent", () => {
    render(<SidebarStorage my={s(153 * MB, 0, null)} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(screen.getByText(`${formatBytes(153 * MB, "en")} used`)).toBeTruthy();
    expect(document.querySelector('[data-testid="sidebar-meter"]')).toBeNull();
    expect(screen.queryByText(/%/)).toBeNull();
  });

  it("at 61% has the title with the percent, a bar in the accent colour, and the figures", () => {
    render(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage (61% full)")).toBeTruthy();
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.dataset.level).toBe("ok");
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe("rgb(254, 204, 0)");
    expect(screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`)).toBeTruthy();
  });

  it("counts the bytes reserved for uploads, as the Storage page does", () => {
    render(<SidebarStorage my={s(GB * 1.2, GB * 0.02, 2 * GB)} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage (61% full)")).toBeTruthy();
  });

  it("draws the bar at the same figure as the text: 99.6% reads 99% and the bar is not full", () => {
    render(<SidebarStorage my={s(996, 0, 1000)} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage (99% full)")).toBeTruthy();
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.inlineSize).toBe("99%");
  });

  it("is the warning colour from 80%, and the error colour at 100% and over", () => {
    const { unmount } = render(<SidebarStorage my={s(850, 0, 1000)} sidebar={SIDEBAR} />);
    expect(document.querySelector('[data-testid="sidebar-meter"]')?.getAttribute("data-level")).toBe("warning");
    unmount();
    const over = render(<SidebarStorage my={s(1000, 0, 1000)} sidebar={SIDEBAR} />);
    expect(document.querySelector('[data-testid="sidebar-meter"]')?.getAttribute("data-level")).toBe("danger");
    expect(screen.getByText("Storage (100% full)")).toBeTruthy();
    over.unmount();
  });

  it("shows the title alone while the figures are not read yet", () => {
    render(<SidebarStorage my={null} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(document.querySelector('[data-testid="sidebar-meter"]')).toBeNull();
  });

  it("gives the collapsed rail the title alone, for its tooltip", async () => {
    const t = (key: string, params?: Record<string, string>) => (key === "nav.storageFull" ? `Storage (${params?.percent}% full)` : "Storage");
    expect(storageTitle(s(610, 0, 1000), t)).toBe("Storage (61% full)");
    expect(storageTitle(s(610, 0, null), t)).toBe("Storage");
    expect(storageTitle(null, t)).toBe("Storage");
  });
});

// On the Storage page its row is the yellow pill: the title and the figures take the on-active colours there, and the bar keeps
// its track visible. Not selected, the block is as it was.
describe("the sidebar's storage block on the selected row", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  it("is the on-active colour for the title, and the figures at 75% of it, when the row is selected", () => {
    render(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} selected />);
    const title = screen.getByText("Storage (61% full)");
    const line = screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`);
    expect(title.style.color).toBe("rgb(13, 33, 66)");
    expect(line.style.color).toBe("rgba(13, 33, 66, 0.75)");
  });

  it("puts the bar's track on the yellow with a ring of the on-active colour outside it, and keeps the accent fill", () => {
    render(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} selected />);
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.style.background).toBe("rgba(13, 33, 66, 0.8)");
    expect(bar?.style.boxShadow).toContain(SIDEBAR.onActive);
    expect(bar?.firstElementChild instanceof HTMLElement && bar.firstElementChild.style.background).toBe("rgb(254, 204, 0)");
  });

  it("is the colours of the sidebar when the row is not selected", () => {
    render(<SidebarStorage my={s(GB * 1.22, 0, 2 * GB)} sidebar={SIDEBAR} />);
    expect(screen.getByText("Storage (61% full)").style.color).toBe("");
    expect(screen.getByText("Storage (61% full)").parentElement?.style.color).toBe("rgb(255, 255, 255)");
    const line = screen.getByText(`${formatBytes(GB * 1.22, "en")} of ${formatBytes(2 * GB, "en")} used`);
    expect(line.style.color).toBe("rgb(169, 193, 230)");
    const bar = document.querySelector<HTMLElement>('[data-testid="sidebar-meter"]');
    expect(bar?.style.background).toBe("rgba(0, 0, 0, 0.35)");
  });
});
