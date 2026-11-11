// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, within } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import Breadcrumbs from "./Breadcrumbs";

const crumbs = [
  { kind: "item" as const, item: { key: "a", label: "Team A", href: "/files/a" } },
  { kind: "item" as const, item: { key: "b", label: "Plans", href: "/files/a/b" } },
];

describe("Breadcrumbs", () => {
  it("draws its separators as icons that mirror in right to left", () => {
    const { container } = render(<Breadcrumbs crumbs={crumbs} />);
    const sep = container.querySelector(".ant-breadcrumb-separator");
    expect(sep?.querySelector("svg.icon-dir")).not.toBeNull();
  });
});

// A trail cut at the 64 nearest folders starts with a mark for the folders above them (decision 140).
describe("the mark for the folders above", () => {
  const marked = [{ kind: "above" as const }, ...crumbs];

  it("comes first, is not a link, and is named for a screen reader", () => {
    const { container } = render(<Breadcrumbs crumbs={marked} />);
    const first = container.querySelector("ol > li");
    expect(first?.querySelector("a")).toBeNull();
    expect(first?.querySelector("[aria-current]")).toBeNull();
    expect(first?.textContent).toContain("…");
    expect(within(first as HTMLElement).getByText("More folders above")).toBeTruthy();
    // The visible mark is not read out as well as its name.
    expect(within(first as HTMLElement).getByText("…").closest('[aria-hidden="true"]')).not.toBeNull();
    // The places after it keep their links.
    expect(screen.getByRole("link", { name: "Team A" }).getAttribute("href")).toBe("/files/a");
  });

  it("shows its name as a tooltip on a mouse's hover", async () => {
    render(<Breadcrumbs crumbs={marked} />);
    fireEvent.mouseEnter(screen.getByTestId("crumbs-above"));
    expect(await screen.findByRole("tooltip")).toHaveProperty("textContent", "More folders above");
  });

  it("takes no drop", () => {
    render(<Breadcrumbs crumbs={marked} />);
    expect(screen.getByTestId("crumbs-above").closest("[data-drop-folder]")).toBeNull();
  });
});

describe("the direction rule", () => {
  it("mirrors the icon-dir icons in a right-to-left page", () => {
    const css = readFileSync(join(process.cwd(), "src/index.css"), "utf8");
    expect(css).toMatch(/:root\[dir="rtl"\] \.icon-dir\s*\{[^}]*transform:\s*scaleX\(-1\)/);
  });
});
