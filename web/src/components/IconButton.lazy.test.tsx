// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Star } from "lucide-react";
import IconButton from "./IconButton";

// mounts counts how many times antd's Tooltip is mounted. The lazy tooltip (decision 128) mounts it only after the first
// pointer-enter or keyboard focus, so a button that nobody has touched has no Tooltip at all.
const mounts = vi.hoisted(() => ({ n: 0 }));
vi.mock("antd", async (orig) => {
  const real = await orig<typeof import("antd")>();
  const { createElement } = await import("react");
  const CountedTooltip = (p: Record<string, unknown>) => {
    mounts.n += 1;
    return createElement(real.Tooltip, p as never);
  };
  return { ...real, Tooltip: CountedTooltip };
});

const tipText = () => waitFor(() => {
  const tip = document.querySelector('[role="tooltip"]');
  if (!tip) throw new Error("no tooltip yet");
  return tip.textContent ?? "";
});

// The anchor is out of the flow: absolute, so it takes no flex space and adds no gap beside the button in a header.
describe("IconButton: the tooltip's anchor takes no layout space (decision 128)", () => {
  it("is absolutely positioned over the button, with no pointer events and no margin", () => {
    const css = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");
    const at = css.indexOf(".icon-btn-anchor {");
    expect(at).toBeGreaterThan(-1);
    const rule = css.slice(at, css.indexOf("}", at));
    expect(rule).toMatch(/position:\s*absolute;/);
    expect(rule).toMatch(/pointer-events:\s*none;/);
    expect(rule).not.toMatch(/margin|gap|width|height/);
  });

  it("is positioned inside its button's wrapper, which is the anchor's containing block", () => {
    const css = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");
    const at = css.indexOf(".icon-btn-wrap {");
    expect(at).toBeGreaterThan(-1);
    const rule = css.slice(at, css.indexOf("}", at));
    expect(rule).toMatch(/position:\s*relative;/);
  });
});

describe("IconButton: the tooltip is mounted on the first hover or focus (decision 128)", () => {
  it("mounts no Tooltip before the pointer or the keyboard reaches the button", () => {
    mounts.n = 0;
    render(<IconButton label="Close" icon={<Star />} />);
    expect(mounts.n).toBe(0);
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("mounts the Tooltip on the first pointer-enter, and shows the same text", async () => {
    mounts.n = 0;
    render(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "mouse" });
    expect(mounts.n).toBeGreaterThan(0);
    expect(await tipText()).toBe("Close");
  });

  it("mounts the Tooltip on a press, and the button is the same element before and after", () => {
    mounts.n = 0;
    render(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    fireEvent.pointerDown(button, { pointerType: "touch" });
    expect(mounts.n).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Close" })).toBe(button);
  });

  it("mounts the Tooltip on keyboard focus, and shows it", async () => {
    mounts.n = 0;
    render(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    fireEvent.keyDown(document.body, { key: "Tab" });
    act(() => button.focus());
    expect(mounts.n).toBeGreaterThan(0);
    expect(await tipText()).toBe("Close");
  });
});
