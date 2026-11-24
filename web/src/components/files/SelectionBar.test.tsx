// SPDX-License-Identifier: AGPL-3.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Share2, Download, Move, Link2, Trash2 } from "lucide-react";
import SelectionBar, { type SelectionAction } from "./SelectionBar";
import { ThemeProvider } from "../../theme/theme";

// Order (decision 68, the owner's model): the clear icon, then the count, then the action icons started right after the
// count, then the More icon. The actions are not pushed to the far end; a spacer takes the rest of the bar.
function renders(onClear = vi.fn(), run = vi.fn()) {
  const actions = [
    { key: "share", label: "Share", icon: <Share2 size={16} />, enabled: true, run },
    { key: "download", label: "Download", icon: <Download size={16} />, enabled: true, run },
    { key: "move", label: "Move to…", icon: <Move size={16} />, enabled: false, run },
  ];
  render(
    <ThemeProvider>
      <SelectionBar
        label="Selection"
        countText="2 selected"
        clearLabel="Clear selection"
        onClear={onClear}
        actions={actions}
        moreMenu={<button type="button" aria-label="More actions">more</button>}
        coarse={false}
      />
    </ThemeProvider>,
  );
  return { bar: screen.getByRole("group"), onClear, run };
}

describe("SelectionBar (decision 68)", () => {
  it("lays out the clear icon, the count, the actions, a spacer and More, in that order", () => {
    const { bar } = renders();
    const kids = Array.from(bar.children) as HTMLElement[];
    const names = kids.map((k) => k.getAttribute("aria-label") ?? k.querySelector("button")?.getAttribute("aria-label") ?? k.getAttribute("data-slot") ?? k.textContent ?? "");
    expect(names).toEqual(["Clear selection", "2 selected", "Share", "Download", "Move to…", "spacer", "More actions"]);
  });

  it("keeps the count from growing, so the actions sit right after it", () => {
    const { bar } = renders();
    const count = (Array.from(bar.children) as HTMLElement[])[1];
    expect(count.style.flex).toBe("0 1 auto");
    const spacer = bar.querySelector('[data-slot="spacer"]') as HTMLElement;
    expect(spacer.style.flex).toMatch(/1 1 auto/);
  });

  it("has no danger box on the delete-style icons, and runs an enabled action", () => {
    const { bar, run } = renders();
    expect(bar.querySelectorAll(".ant-btn-dangerous").length).toBe(0);
    fireEvent.click(screen.getByRole("button", { name: "Share" }));
    expect(run).toHaveBeenCalledOnce();
  });

  it("clears the selection from the X icon", () => {
    const { onClear } = renders();
    fireEvent.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(onClear).toHaveBeenCalledOnce();
  });
});

// Decision 185: the bar shows as many of its ordered actions as fit its own width, and the rest go to the caller's overflow
// (the top of More), in order. The count shows its whole text, and the fit reserves its measured width (48 px floor). The
// slot is the icon button plus the gap: 44 + 4 on a coarse pointer, 40 + 4 otherwise. The fixed part is Clear + the count +
// More + the bar's padding + the three gaps between those four: 2 * button + count + 2 * 4 + 3 * 4, so with the 48 px floor
// 156 on a finger and 148 with a mouse.
describe("SelectionBar fits its own width (decision 201)", () => {
  const saved = globalThis.ResizeObserver;
  afterEach(() => {
    globalThis.ResizeObserver = saved;
  });

  // reportWidth installs a ResizeObserver that tells the bar its border-box width as soon as it is observed.
  const reportWidth = (width: number) => {
    globalThis.ResizeObserver = class {
      private cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe(target: Element) {
        this.cb(
          [{ target, borderBoxSize: [{ inlineSize: width, blockSize: 52 }], contentRect: { width: width - 8 } } as unknown as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  };

  // The page's ordered actions, with Move disabled so the overflow keeps a disabled action disabled.
  const actions = (run: () => void): SelectionAction[] => [
    { key: "share", label: "Share", icon: <Share2 size={16} />, enabled: true, run },
    { key: "copyLink", label: "Copy link", icon: <Link2 size={16} />, enabled: true, run },
    { key: "download", label: "Download", icon: <Download size={16} />, enabled: true, run },
    { key: "move", label: "Move to…", icon: <Move size={16} />, enabled: false, run },
    { key: "delete", label: "Delete", icon: <Trash2 size={16} />, enabled: true, run },
  ];

  // more is the caller's More: its button, the hidden actions it is given (in order, ahead of its own entry), then Details.
  const more = (hidden: SelectionAction[]) => (
    <span data-testid="more-menu">
      <button type="button" aria-label="More actions">more</button>
      {hidden.map((a) => (
        <button key={a.key} type="button" role="menuitem" disabled={!a.enabled} onClick={a.run}>{a.label}</button>
      ))}
      <button type="button" role="menuitem">Details</button>
    </span>
  );

  const fitRenders = (opts: { width?: number; coarse?: boolean; run?: () => void; countText?: string } = {}) => {
    const run = opts.run ?? vi.fn();
    if (opts.width !== undefined) reportWidth(opts.width);
    const tree = (countText: string) => (
      <ThemeProvider>
        <SelectionBar
          label="Selection"
          countText={countText}
          clearLabel="Clear selection"
          onClear={vi.fn()}
          actions={actions(run)}
          moreMenu={more([])}
          overflow={more}
          hideOrder={["copyLink", "move", "delete", "download", "share"]}
          coarse={opts.coarse ?? false}
        />
      </ThemeProvider>
    );
    const view = render(tree(opts.countText ?? "1 selected"));
    return { bar: screen.getByRole("group"), run, setCountText: (countText: string) => view.rerender(tree(countText)) };
  };

  // The bar's own buttons: Clear, the action icons, More. The overflow's entries are menuitems, not buttons of the bar.
  const buttonNames = (bar: HTMLElement) =>
    within(bar).getAllByRole("button").map((b) => b.getAttribute("aria-label") ?? b.textContent?.trim() ?? "");
  const entryNames = () => screen.getAllByRole("menuitem").map((m) => m.textContent?.trim() ?? "");

  it("a width that fits every action keeps all the icons, and More holds only its own entries", () => {
    const { bar } = fitRenders({ width: 800 });
    expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Copy link", "Download", "Move to…", "Delete", "More actions"]);
    expect(entryNames()).toEqual(["Details"]);
  });

  it("a 360 px bar on a coarse pointer shows floor((360 - 156) / 48) = 4 icons: Copy link goes to More first and Delete keeps its icon (decision 68's rule)", () => {
    const { bar } = fitRenders({ width: 360, coarse: true });
    expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "Move to…", "Delete", "More actions"]);
    expect(entryNames()).toEqual(["Copy link", "Details"]);
    const count = (Array.from(bar.children) as HTMLElement[])[1];
    expect(count.style.minWidth).toBe("48px");
  });

  it("a fine-pointer bar of 260 px shows floor((260 - 148) / 44) = 2 icons: Share and Download stay, the rest go to More in display order", () => {
    const { bar } = fitRenders({ width: 260 });
    expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "More actions"]);
    expect(entryNames()).toEqual(["Copy link", "Move to…", "Delete", "Details"]);
  });

  it("when no icon fits, every action is in More, a disabled one stays disabled, and an entry runs its action", () => {
    const run = vi.fn();
    // 150 is below the fixed 156: the formula gives -1, and the bar keeps 0 icons, never fewer.
    const { bar } = fitRenders({ width: 150, coarse: true, run });
    expect(buttonNames(bar)).toEqual(["Clear selection", "More actions"]);
    expect(entryNames()).toEqual(["Share", "Copy link", "Download", "Move to…", "Delete", "Details"]);
    const move = screen.getByRole("menuitem", { name: "Move to…" }) as HTMLButtonElement;
    expect(move.disabled).toBe(true);
    fireEvent.click(move);
    expect(run).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("menuitem", { name: "Share" }));
    expect(run).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(run).toHaveBeenCalledTimes(2);
  });

  it("with no ResizeObserver every action keeps its icon", () => {
    globalThis.ResizeObserver = undefined as unknown as typeof ResizeObserver;
    const { bar } = fitRenders({ width: undefined });
    expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Copy link", "Download", "Move to…", "Delete", "More actions"]);
    expect(entryNames()).toEqual(["Details"]);
  });

  // The first draw keeps every icon until the width is measured, and the measure lays the row out once with them all. The bar
  // clips that draw so the icons past its edge cannot widen the page: on a phone a wider page grows the layout viewport and
  // zooms out, clamping the window's scrollY down, which closes a menu opened next (the More tap). clip, not hidden, so a focus
  // on an icon does not scroll the bar sideways.
  it("clips its own overflow, so the unmeasured first draw cannot widen the page and clamp the scroll", () => {
    const { bar } = fitRenders({ width: undefined });
    expect(bar.style.overflow).toBe("clip");
  });

  // Decision 242: the count shows its whole text, and the fit reserves its measured width (scrollWidth, padding included),
  // with 48 px as the floor. jsdom does not lay out, so the count's natural width is stated on the prototype here; the
  // saved getter is restored after each test.
  describe("the count shows its whole text (decision 242)", () => {
    afterEach(() => {
      delete (HTMLElement.prototype as { scrollWidth?: number }).scrollWidth;
    });

    // measureCount states the natural width jsdom cannot lay out: a number for every element, or a read per element.
    const measureCount = (width: number | ((el: HTMLElement) => number)) => {
      Object.defineProperty(HTMLElement.prototype, "scrollWidth", {
        configurable: true,
        get(this: HTMLElement) {
          return typeof width === "function" ? width(this) : width;
        },
      });
    };
    const countOf = (bar: HTMLElement) => (Array.from(bar.children) as HTMLElement[])[1];

    it("the count has no 48 px max width and holds its full text", () => {
      const { bar } = fitRenders({ width: 800 });
      const count = countOf(bar);
      expect(count.style.maxWidth).toBe("");
      expect(count.textContent).toBe("1 selected");
      expect(count.style.minWidth).toBe("48px");
      expect(count.style.flex).toBe("0 1 auto");
      expect(count.style.whiteSpace).toBe("nowrap");
      expect(count.getAttribute("title")).toBe("1 selected");
      expect(count.getAttribute("aria-live")).toBe("polite");
    });

    it("a count measured at 90 px reserves 90: one icon fewer than with the 48 px floor, and the dropped one is in More", () => {
      // 300 px with a mouse: floor((300 - 148) / 44) = 3 icons with the floor, floor((300 - 190) / 44) = 2 with 90.
      const plain = fitRenders({ width: 300 });
      expect(buttonNames(plain.bar)).toEqual(["Clear selection", "Share", "Download", "Delete", "More actions"]);
      cleanup();
      measureCount((el) => (el.getAttribute("aria-live") === "polite" ? 90 : 0));
      const { bar } = fitRenders({ width: 300 });
      expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "More actions"]);
      expect(entryNames()).toEqual(["Copy link", "Move to…", "Delete", "Details"]);
    });

    it("a count text change measures the new text's width", () => {
      measureCount((el) => (el.textContent === "10 selected" ? 90 : el.textContent === "1 selected" ? 60 : 0));
      const { bar, setCountText } = fitRenders({ width: 300 });
      // "1 selected" measures 60: floor((300 - 160) / 44) = 3 icons.
      expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "Delete", "More actions"]);
      setCountText("10 selected");
      // "10 selected" measures 90: floor((300 - 190) / 44) = 2 icons, and Delete joins More.
      expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "More actions"]);
      expect(entryNames()).toEqual(["Copy link", "Move to…", "Delete", "Details"]);
    });

    it("a count that cannot be measured keeps the 48 px floor in the fit", () => {
      // jsdom's scrollWidth is 0: not a measurement, so the fit keeps 48 and 300 px shows floor((300 - 148) / 44) = 3.
      const { bar } = fitRenders({ width: 300 });
      expect(buttonNames(bar)).toEqual(["Clear selection", "Share", "Download", "Delete", "More actions"]);
      expect(entryNames()).toEqual(["Copy link", "Move to…", "Details"]);
    });
  });
});
