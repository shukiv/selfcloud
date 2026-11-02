// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Share2, Download, Move } from "lucide-react";
import SelectionBar from "./SelectionBar";
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
    expect(["none", "0 0 auto"]).toContain(count.style.flex);
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
