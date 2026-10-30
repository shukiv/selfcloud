// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
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

describe("the direction rule", () => {
  it("mirrors the icon-dir icons in a right-to-left page", () => {
    const css = readFileSync(join(process.cwd(), "src/index.css"), "utf8");
    expect(css).toMatch(/:root\[dir="rtl"\] \.icon-dir\s*\{[^}]*transform:\s*scaleX\(-1\)/);
  });
});
