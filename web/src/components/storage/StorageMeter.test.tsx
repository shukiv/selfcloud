// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import { theme as antdTheme } from "antd";
import { describe, expect, it } from "vitest";
import { translate } from "../../lib/i18n";
import { TYPE_COLOURS } from "../../lib/files/iconType";
import { antdTokens } from "../../theme/theme";
import { PALETTE, SIDEBAR } from "../../theme/tokens";
import StorageMeter, { SEGMENT_COLOURS } from "./StorageMeter";

const MB = 1_000_000;
const figures = { used_bytes: 153 * MB, reserved_bytes: 0, quota_bytes: 1000 * MB, available_bytes: 847 * MB };
const breakdown = { versions_bytes: 4 * MB, versions_files: 6, trash_bytes: 8 * MB, trash_files: 3 };

// lum, ratio, rgba and over: the WCAG contrast of two colours, as iconType.test.ts measures them.
function lum(hex: string): number {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((x) => (x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}
function rgba(css: string): [number, number, number, number] {
  const m = css.match(/rgba?\(([^)]+)\)/);
  if (m) {
    const [r, g, b, a = "1"] = m[1].split(",").map((x) => x.trim());
    return [Number(r), Number(g), Number(b), Number(a)];
  }
  const hex = css.replace("#", "");
  return [parseInt(hex.slice(0, 2), 16), parseInt(hex.slice(2, 4), 16), parseInt(hex.slice(4, 6), 16), 1];
}
function over(fill: string, ground: string): string {
  const [r, g, b, a] = rgba(fill);
  const [R, G, B] = rgba(ground);
  return "#" + [r, g, b].map((c, i) => Math.round(c * a + [R, G, B][i] * (1 - a)).toString(16).padStart(2, "0")).join("");
}

describe("StorageMeter (decision 79, one meter)", () => {
  it("shows the amount held, the limit and what is free, under the figure", () => {
    render(<StorageMeter figures={figures} breakdown={breakdown} />);
    expect(screen.getByText("153 MB")).toBeTruthy();
    expect(screen.getByText(translate("en", "storage.meter.ofLimit", { limit: "1 GB", free: "847 MB" }))).toBeTruthy();
    expect(screen.getByText(translate("en", "storage.mine"))).toBeTruthy();
  });

  it("splits the bar into files, old versions and the trash, and lists each part's size in the legend", () => {
    const { container } = render(<StorageMeter figures={figures} breakdown={breakdown} />);
    const parts = Array.from(container.querySelectorAll("[data-part]")).map((el) => el.getAttribute("data-part"));
    expect(parts).toEqual(["files", "versions", "trash", "free"]);
    const legend = container.querySelector(".storage-legend") as HTMLElement;
    expect(within(legend).getByText("141 MB")).toBeTruthy();
    expect(within(legend).getByText("4 MB")).toBeTruthy();
    expect(within(legend).getByText("8 MB")).toBeTruthy();
    expect(within(legend).getByText(translate("en", "storage.reserved"))).toBeTruthy();
  });

  it("has a text alternative for the whole bar, with every part and its size", () => {
    render(<StorageMeter figures={figures} breakdown={breakdown} />);
    const bar = screen.getByRole("img", { name: /of 1 GB used/ });
    const alt = bar.getAttribute("aria-label") ?? "";
    expect(alt).toContain("153 MB of 1 GB used");
    expect(alt).toContain("files 141 MB");
    expect(alt).toContain("old versions 4 MB");
    expect(alt).toContain("in the trash 8 MB");
    expect(alt).toContain("reserved by uploads 0 bytes");
    expect(alt).toContain("free 847 MB");
  });

  it("with no limit: says so, has no free part, and the alternative says no limit", () => {
    const { container } = render(<StorageMeter figures={{ ...figures, quota_bytes: null, available_bytes: null }} breakdown={breakdown} />);
    expect(screen.getByText(translate("en", "storage.meter.noLimit"))).toBeTruthy();
    expect(container.querySelector('[data-part="free"]')).toBeNull();
    expect(screen.getByRole("img", { name: /no limit/ })).toBeTruthy();
    expect(screen.queryByText(translate("en", "storage.free"))).toBeNull();
  });

  it("leaves a zero part out of the bar but keeps it in the legend", () => {
    const { container } = render(<StorageMeter figures={figures} breakdown={{ ...breakdown, trash_bytes: 0, trash_files: 0 }} />);
    expect(container.querySelector('[data-part="trash"]')).toBeNull();
    expect(screen.getByText(translate("en", "storage.inTrash"))).toBeTruthy();
  });
});

// The bar's parts against their neighbours: each part is at least 3:1 against the track it sits on and against the card,
// in both modes (WCAG non-text contrast). The legend's labels are text: 4.5:1 on the card.
describe("StorageMeter colours (decision 79, contrast)", () => {
  for (const mode of ["light", "dark"] as const) {
    it(`keeps every part at 3:1 against the track and the card, and the labels at 4.5:1, in ${mode} mode`, () => {
      const p = PALETTE[mode];
      const tokens = antdTheme.getDesignToken(antdTokens(mode, p, SIDEBAR));
      const card = tokens.colorBgContainer as string;
      const track = over(tokens.colorFillSecondary as string, card);
      const files = tokens.colorPrimaryText as string;
      const parts: Record<string, string> = {
        files,
        versions: TYPE_COLOURS.blue,
        trash: TYPE_COLOURS.orange,
        reserved: TYPE_COLOURS.grey,
      };
      for (const [name, hex] of Object.entries(parts)) {
        expect(ratio(hex, track), `${name} against the track in ${mode}`).toBeGreaterThanOrEqual(3);
        expect(ratio(hex, card), `${name} against the card in ${mode}`).toBeGreaterThanOrEqual(3);
      }
      expect(SEGMENT_COLOURS.files).toBe("var(--ant-color-primary-text)");
      expect(ratio(tokens.colorTextSecondary as string, card), `labels in ${mode}`).toBeGreaterThanOrEqual(4.5);
    });
  }
});
