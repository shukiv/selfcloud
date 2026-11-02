// SPDX-License-Identifier: AGPL-3.0-or-later

import { createRef } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Star } from "lucide-react";
import IconButton from "./IconButton";
import { ThemeProvider } from "../theme/theme";
import { LIGHT } from "../theme/tokens";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// The stylesheet's text, read from the web root that vitest runs in: the vitest run does not load CSS, and a ?raw import is empty.
const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

// tipText waits for the tooltip popup and returns its text (antd draws it in the body; role queries miss it while it animates).
const tipText = () => waitFor(() => {
  const tip = document.querySelector('[role="tooltip"]');
  if (!tip) throw new Error("no tooltip yet");
  return tip.textContent ?? "";
});

// noTip waits a moment, then checks that no tooltip has opened.
const noTip = async () => {
  await new Promise((r) => setTimeout(r, 60));
  expect(document.querySelector('.ant-tooltip:not(.ant-tooltip-hidden)')).toBeNull();
};

// A mouse pointer enters the button's wrapper, as a browser delivers it (the wrapper is the element with the handlers).
const mouseEnter = (button: HTMLElement) => fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "mouse" });
const mouseLeave = (button: HTMLElement) => fireEvent.pointerLeave(button.parentElement as HTMLElement, { pointerType: "mouse" });

// rule returns the text of the first CSS rule whose selector starts with the given text, inside the given block (or the sheet).
function rule(css: string, selector: string, inside = ""): string {
  const scope = inside ? css.slice(css.indexOf(inside)) : css;
  const start = scope.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`no rule for ${selector}`);
  return scope.slice(start, scope.indexOf("}", start) + 1);
}

// rgb turns a #hex colour into the form the DOM reports for a style's colour.
const rgb = (hex: string) => `rgb(${[1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(", ")})`;

const wrap = (node: React.ReactNode) => render(<ThemeProvider>{node}</ThemeProvider>);

describe("IconButton (decision 68)", () => {
  it("names the button by its label and shows the same text as its tooltip", async () => {
    wrap(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    expect(button.getAttribute("aria-label")).toBe("Close");
    mouseEnter(button);
    expect(await tipText()).toBe("Close");
  });

  it("shows a separate tooltip text when one is given, and keeps the label as the name", async () => {
    wrap(<IconButton label="Star Report.pdf" tip="Star" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Star Report.pdf" });
    mouseEnter(button);
    expect(await tipText()).toBe("Star");
    expect(screen.getByRole("button", { name: "Star Report.pdf" })).toBeTruthy();
  });

  it("is a text button with no border and a round shape", () => {
    wrap(<IconButton label="Play" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Play" });
    expect(button.className).toMatch(/ant-btn-text/);
    expect(button.className).toMatch(/ant-btn-circle/);
    expect(button.className).toMatch(/icon-btn/);
  });

  // Review fix (decision 68): the border is in the stylesheet, not only the class, so the rule itself is read.
  it("has no border in its CSS rule, and is 40 px (44 px on a coarse pointer)", () => {
    const base = rule(indexCss, ".icon-btn.ant-btn.ant-btn");
    expect(base).toMatch(/border:\s*0;/);
    expect(base).toMatch(/\n\s*width:\s*40px;/);
    expect(base).toMatch(/\n\s*height:\s*40px;/);
    const coarse = indexCss.slice(indexCss.indexOf("@media (pointer: coarse)", indexCss.indexOf(".icon-btn-wrap")));
    const coarseRule = rule(coarse, ".icon-btn.ant-btn.ant-btn");
    expect(coarseRule).toMatch(/\n\s*width:\s*44px;/);
    expect(coarseRule).toMatch(/\n\s*height:\s*44px;/);
  });

  // A press opens a menu, so the tooltip is not drawn over it; it comes back once the pointer has left and come again.
  it("shows no tooltip after a click, until the pointer leaves", async () => {
    wrap(<IconButton label="More" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "More" });
    mouseEnter(button);
    expect(await tipText()).toBe("More");
    fireEvent.click(button);
    // antd keeps a closed popup in the page while it animates out, so the test looks for an open one.
    await waitFor(() => expect(document.querySelector('.ant-tooltip:not(.ant-tooltip-hidden)')).toBeNull());
    mouseLeave(button);
    mouseEnter(button);
    expect(await tipText()).toBe("More");
  });

  // Review fix: a tap on a finger fires compatibility mouse events with no click. Only a mouse opens the tooltip, so a tap
  // neither leaves it open nor flashes it.
  it("does not open the tooltip for a touch, or for the compatibility mouse events a tap fires", async () => {
    wrap(<IconButton label="Next file" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Next file" });
    fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "touch" });
    fireEvent.pointerDown(button, { pointerType: "touch" });
    fireEvent.mouseEnter(button);
    fireEvent.mouseOver(button);
    await noTip();
    mouseEnter(button);
    expect(await tipText()).toBe("Next file");
    // A touch's leave does not close a mouse's hover: the popup is not in its leave motion (antd's close class, which jsdom keeps).
    fireEvent.pointerLeave(button.parentElement as HTMLElement, { pointerType: "touch" });
    await new Promise((r) => setTimeout(r, 100));
    expect(document.querySelector('.ant-tooltip:not(.ant-tooltip-hidden):not([class*="-leave"])')).not.toBeNull();
  });

  it("sets aria-pressed and the pressed class when pressed", () => {
    wrap(<IconButton label="Playlist" pressed icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Playlist" });
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(button.className).toMatch(/icon-btn--pressed/);
  });

  // Review fix: the pointer events arrive on the disabled button itself (a browser may drop them there), and the wrapper
  // still opens the tooltip. The stylesheet makes the button pass its events to the wrapper too.
  it("is disabled and still shows its tooltip, when the pointer is over the disabled button", async () => {
    wrap(<IconButton label="Next file" disabled icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Next file" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.pointerEnter(button, { pointerType: "mouse" });
    expect(await tipText()).toBe("Next file");
    expect(rule(indexCss, ".icon-btn-wrap > .icon-btn:disabled")).toMatch(/pointer-events:\s*none;/);
  });

  // Review fix: a key press, then focus, opens the tooltip; a mouse press, then focus (a click on a button focuses it), does
  // not. The decision is made from the last input, not from :focus-visible, so it does not depend on the engine.
  it("opens the tooltip on keyboard focus, after a key press", async () => {
    wrap(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    fireEvent.keyDown(document.body, { key: "Tab" });
    act(() => button.focus());
    expect(await tipText()).toBe("Close");
  });

  it("does not open the tooltip on a focus that follows a mouse press, even where :focus-visible matches", async () => {
    const real = Element.prototype.matches;
    const spy = vi.spyOn(Element.prototype, "matches").mockImplementation(function (this: Element, selector: string) {
      return selector === ":focus-visible" ? true : real.call(this, selector);
    });
    try {
      wrap(<IconButton label="Close" icon={<Star />} />);
      const button = screen.getByRole("button", { name: "Close" });
      fireEvent.pointerDown(button, { pointerType: "mouse" });
      act(() => button.focus());
      await noTip();
    } finally {
      spy.mockRestore();
    }
  });

  it("closes the tooltip when the keyboard focus leaves", async () => {
    wrap(<IconButton label="Close" icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Close" });
    fireEvent.keyDown(document.body, { key: "Tab" });
    act(() => button.focus());
    expect(await tipText()).toBe("Close");
    act(() => button.blur());
    await waitFor(() => expect(document.querySelector('.ant-tooltip:not(.ant-tooltip-hidden)')).toBeNull());
  });

  it("colours the glyph for danger in the error colour, and draws no box", () => {
    wrap(<IconButton label="Delete" danger icon={<Star />} />);
    const button = screen.getByRole("button", { name: "Delete" });
    expect(button.style.color).toBe(rgb(LIGHT.danger));
    expect(button.className).not.toMatch(/dangerous/);
  });

  it("puts the tooltip in the given container (full screen)", async () => {
    const host = document.createElement("div");
    host.id = "stage";
    document.body.appendChild(host);
    wrap(<IconButton label="Mute" popupHost={() => host} icon={<Star />} />);
    mouseEnter(screen.getByRole("button", { name: "Mute" }));
    await waitFor(() => expect(host.querySelector('[role="tooltip"]')).not.toBeNull());
    host.remove();
  });

  it("forwards its ref and its click, so a menu can anchor on it", () => {
    const ref = createRef<HTMLButtonElement>();
    const onClick = vi.fn();
    wrap(<IconButton ref={ref} label="Menu" icon={<Star />} onClick={onClick} data-testid="x" />);
    expect(ref.current).toBe(screen.getByTestId("x"));
    fireEvent.click(screen.getByRole("button", { name: "Menu" }));
    expect(onClick).toHaveBeenCalledOnce();
  });
});
