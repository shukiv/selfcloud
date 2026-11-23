// SPDX-License-Identifier: AGPL-3.0-or-later

// A preset list (Starred, Trash, Search, the Manage and admin tables) draws its rows as My files does: a press is one update,
// and a row's mark, icons and ⋮ button are drawn again only when that row's own state changes (decision 128).
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Listing, { type ItemListingProps, type ListAction } from "./Listing";
import { presetMarkEqual } from "./PresetMark";
import { desktopViewport } from "../../test/viewport";

// buttons counts the renders of each icon button by its label, and checks the renders of each row's mark by its name. The mark is
// a memoised component (the icon is drawn outside it, fresh on every pass), so its render counts only the row's own pass (decision 128).
const buttons = vi.hoisted(() => ({} as Record<string, number>));
const checks = vi.hoisted(() => ({} as Record<string, number>));
vi.mock("../IconButton", async (orig) => {
  const real = await orig<typeof import("../IconButton")>();
  const { createElement } = await import("react");
  return {
    default: (p: Parameters<typeof real.default>[0]) => {
      buttons[p.label] = (buttons[p.label] ?? 0) + 1;
      return createElement(real.default, p);
    },
  };
});
vi.mock("./PresetMark", async (orig) => {
  const real = await orig<typeof import("./PresetMark")>();
  const { createElement, memo } = await import("react");
  return {
    ...real,
    default: memo((p: Parameters<typeof real.default>[0]) => {
      checks[p.name] = (checks[p.name] ?? 0) + 1;
      return createElement(real.default, p);
    }, real.presetMarkEqual),
  };
});

type Row = { id: string; name: string };
const ROWS = 20;
const rows: Row[] = Array.from({ length: ROWS }, (_, i) => ({ id: `r${i}`, name: `Row ${i}` }));

// nameRenders counts how many passes the table made over each row's name column: one pass is one render of the list.
const nameRenders = vi.hoisted(() => ({} as Record<string, number>));
const columns: ItemListingProps<Row>["columns"] = [
  {
    key: "name",
    title: "Name",
    render: (r) => {
      nameRenders[r.id] = (nameRenders[r.id] ?? 0) + 1;
      return r.name;
    },
  },
];

const reset = () => {
  for (const o of [buttons, checks, nameRenders]) for (const k of Object.keys(o)) delete o[k];
};

type Over = Partial<ItemListingProps<Row>>;
// actions are three inline icons for every row; each call makes new objects and new functions, as a page does on every render.
const actionsOf =
  (run: (r: Row, key: string) => void = () => undefined, over: (r: Row, key: string) => Partial<ListAction> = () => ({})) =>
  (r: Row): ListAction[] =>
    ["share", "download", "rename"].map((key) => ({ key, label: `${key} ${r.name}`, icon: <span>{key[0]}</span>, run: () => run(r, key), ...over(r, key) }));

function props(over: Over = {}): ItemListingProps<Row> {
  return {
    items: rows,
    label: "Things",
    rowLabel: (r) => r.name,
    columns,
    selectable: true,
    icon: (r) => <span data-icon={r.id} />,
    actions: actionsOf(),
    menu: (r) => [{ key: "info", label: `Info ${r.name}`, onSelect: () => undefined }],
    empty: { title: "Nothing here" },
    ...over,
  };
}

const rowOf = (name: string) => screen.getByRole("row", { name });
const sumFor = (counts: Record<string, number>, name: string) =>
  Object.entries(counts)
    .filter(([label]) => label.endsWith(` ${name}`))
    .reduce((s, [, n]) => s + n, 0);
// rowLabels are the counted labels that belong to a row: the header's select-all box and the selection bar's buttons are not rows.
const rowLabels = (counts: Record<string, number>) => Object.keys(counts).filter((label) => /Row \d+$/.test(label));
const othersSum =(counts: Record<string, number>, except: string[]) =>
  rows.filter((r) => !except.includes(r.name)).reduce((s, r) => s + sumFor(counts, r.name), 0);
// othersChecks sums the mark renders of every row but the ones named: the mark is counted by the row's own name.
const othersChecks = (except: string[]) => rows.filter((r) => !except.includes(r.name)).reduce((s, r) => s + (checks[r.name] ?? 0), 0);

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
});
afterEach(() => {
  undoView();
  reset();
});

describe("a preset list draws a row again only when its own state changes (decision 128)", () => {
  it("a click that adds a row to the selection draws that row's mark, and no other row's mark or buttons", () => {
    render(<Listing<Row> {...props()} />);
    fireEvent.click(rowOf("Row 0"));
    reset();
    fireEvent.click(rowOf("Row 4"), { ctrlKey: true });
    expect(rowOf("Row 4").getAttribute("aria-selected")).toBe("true");
    expect(checks["Row 4"]).toBeGreaterThan(0);
    expect(othersChecks(["Row 4"])).toBe(0);
    expect(rowLabels(buttons)).toEqual([]);
  });

  it("a press (focus on the press, then the click) is one pass over the rows", () => {
    render(<Listing<Row> {...props()} />);
    const row = rowOf("Row 7");
    reset();
    fireEvent.pointerDown(row, { pointerType: "mouse" });
    act(() => row.focus());
    fireEvent.pointerUp(row, { pointerType: "mouse" });
    fireEvent.click(row);
    expect(row.getAttribute("aria-selected")).toBe("true");
    expect(nameRenders["r7"]).toBe(1);
  });

  it("a press on a row's ⋮ button focuses the row with its click", () => {
    render(<Listing<Row> {...props()} />);
    const more = screen.getByRole("button", { name: "More actions for Row 7" });
    fireEvent.pointerDown(more, { pointerType: "mouse" });
    act(() => more.focus());
    fireEvent.pointerUp(more, { pointerType: "mouse" });
    fireEvent.click(more);
    expect(rowOf("Row 7").getAttribute("tabindex")).toBe("0");
  });

  it("a press whose click never reaches the row (a control stopped it) still focuses the row", async () => {
    render(<Listing<Row> {...props()} />);
    const row = rowOf("Row 8");
    fireEvent.pointerDown(row, { pointerType: "mouse" });
    act(() => row.focus());
    fireEvent.pointerUp(row, { pointerType: "mouse" });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10));
    });
    expect(row.getAttribute("tabindex")).toBe("0");
  });

  it("a keyboard focus on a row is still its own pass, and moves the roving stop", () => {
    render(<Listing<Row> {...props()} />);
    const row = rowOf("Row 3");
    act(() => row.focus());
    expect(row.getAttribute("tabindex")).toBe("0");
    expect(rowOf("Row 0").getAttribute("tabindex")).toBe("-1");
  });

  it("a press released on another row keeps the focus on the row it was pressed on", () => {
    render(<Listing<Row> {...props()} />);
    const down = rowOf("Row 5");
    fireEvent.pointerDown(down, { pointerType: "mouse" });
    act(() => down.focus());
    fireEvent.pointerUp(rowOf("Row 9"), { pointerType: "mouse" });
    expect(down.getAttribute("tabindex")).toBe("0");
  });

  it("a right click on one row opens its menu and draws no other row's buttons", async () => {
    render(<Listing<Row> {...props()} />);
    reset();
    fireEvent.contextMenu(rowOf("Row 2"));
    expect(await screen.findByText("Info Row 2")).toBeTruthy();
    expect(othersSum(buttons, ["Row 2"])).toBe(0);
    expect(othersChecks(["Row 2"])).toBe(0);
  });

  it("a hover on a row draws no row's buttons again", () => {
    render(<Listing<Row> {...props()} />);
    reset();
    fireEvent.pointerEnter(rowOf("Row 6"), { pointerType: "mouse" });
    fireEvent.pointerLeave(rowOf("Row 6"), { pointerType: "mouse" });
    expect(rowLabels(buttons)).toEqual([]);
  });

  it("a page render with new callbacks and the same data draws no row's buttons or mark again", () => {
    const { rerender } = render(<Listing<Row> {...props()} />);
    reset();
    rerender(<Listing<Row> {...props()} />);
    expect(rowLabels(buttons)).toEqual([]);
    expect(rowLabels(checks)).toEqual([]);
  });
});

describe("a preset row's icon follows its item under the selection mark", () => {
  it("a row whose icon changes keeps its id, name and selection but draws the new icon", () => {
    let color = "red";
    const icon = (r: Row) => <span data-icon={r.id} data-color={color} />;
    const { rerender } = render(<Listing<Row> {...props({ icon })} />);
    const iconOf = (name: string) => rowOf(name).querySelector("[data-icon]") as HTMLElement;
    expect(iconOf("Row 0").getAttribute("data-color")).toBe("red");
    fireEvent.click(rowOf("Row 0"));
    expect(rowOf("Row 0").getAttribute("aria-selected")).toBe("true");
    color = "blue";
    rerender(<Listing<Row> {...props({ icon })} />);
    expect(rowOf("Row 0").getAttribute("aria-selected")).toBe("true");
    expect(iconOf("Row 0").getAttribute("data-color")).toBe("blue");
  });
});

describe("a preset row's actions follow the page (decision 128)", () => {
  it("an inline icon runs the page's latest action, not the one the row was drawn with", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = render(<Listing<Row> {...props({ actions: actionsOf(first) })} />);
    rerender(<Listing<Row> {...props({ actions: actionsOf(second) })} />);
    fireEvent.click(screen.getByRole("button", { name: "share Row 1" }));
    expect(second).toHaveBeenCalledWith(rows[1], "share");
    expect(first).not.toHaveBeenCalled();
  });

  it("a ⋮ entry runs the page's latest entry", async () => {
    const first = vi.fn();
    const second = vi.fn();
    const menu = (run: () => void) => (r: Row) => [{ key: "info", label: `Info ${r.name}`, onSelect: run }];
    const { rerender } = render(<Listing<Row> {...props({ menu: menu(first) })} />);
    rerender(<Listing<Row> {...props({ menu: menu(second) })} />);
    fireEvent.click(screen.getByRole("button", { name: "More actions for Row 1" }));
    fireEvent.click(await screen.findByText("Info Row 1"));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it("an action that turns disabled is drawn disabled, and does not run", () => {
    const run = vi.fn();
    const { rerender } = render(<Listing<Row> {...props({ actions: actionsOf(run) })} />);
    rerender(<Listing<Row> {...props({ actions: actionsOf(run, (r, key) => (r.id === "r1" && key === "share" ? { disabled: true } : {})) })} />);
    const share = screen.getByRole("button", { name: "share Row 1" });
    expect(share.getAttribute("aria-disabled")).toBe("true");
    fireEvent.click(share);
    expect(run).not.toHaveBeenCalled();
  });

  it("an action whose label changes shows the new label", () => {
    const { rerender } = render(<Listing<Row> {...props()} />);
    rerender(<Listing<Row> {...props({ actions: actionsOf(undefined, (r, key) => (r.id === "r1" && key === "share" ? { label: "Unshare Row 1" } : {})) })} />);
    expect(screen.queryByRole("button", { name: "share Row 1" })).toBeNull();
    expect(screen.getByRole("button", { name: "Unshare Row 1" })).toBeTruthy();
  });

  it("a column the page renders from its own state shows the new state", () => {
    let when = "today";
    const withWhen: ItemListingProps<Row>["columns"] = [...columns, { key: "when", title: "When", render: () => when }];
    const { rerender } = render(<Listing<Row> {...props({ columns: withWhen })} />);
    expect(screen.getAllByText("today").length).toBe(ROWS);
    when = "yesterday";
    rerender(<Listing<Row> {...props({ columns: [...withWhen] })} />);
    expect(screen.getAllByText("yesterday").length).toBe(ROWS);
  });
});

// While anything is selected, every other preset row shows an empty outline circle in its mark; a click on the circle selects
// that row too, and a click on the check unselects it. With nothing selected no circle shows.
describe("a preset list: the empty circle while anything is selected", () => {
  it("shows no circle on any row when nothing is selected", () => {
    render(<Listing<Row> {...props()} />);
    expect(document.querySelector(".empty-circle")).toBeNull();
  });

  it("shows the circle in every other row's mark, and the check on the selected row", () => {
    render(<Listing<Row> {...props()} />);
    fireEvent.click(rowOf("Row 4"));
    expect(rowOf("Row 4").querySelector(".check-badge")).not.toBeNull();
    expect(rowOf("Row 4").querySelector(".empty-circle")).toBeNull();
    for (const name of ["Row 0", "Row 1", "Row 3", "Row 5", "Row 19"]) {
      const mark = rowOf(name).querySelector("button.row-mark") as HTMLElement;
      const circle = mark.querySelector(".empty-circle") as HTMLElement;
      expect(circle).not.toBeNull();
      expect(circle.style.width).toBe("17px");
      expect(circle.style.height).toBe("17px");
      expect(circle.style.border).toContain("2px solid");
      expect(mark.getAttribute("aria-pressed")).toBe("false");
      expect(mark.querySelector(".check-badge")).toBeNull();
    }
  });

  it("a click on a circle adds the row to the selection and opens nothing; a click on the check takes it away", () => {
    const onOpen = vi.fn();
    render(<Listing<Row> {...props({ onOpen })} />);
    fireEvent.click(rowOf("Row 4"), { ctrlKey: true });
    fireEvent.click(within(rowOf("Row 2")).getByRole("button", { name: "Select Row 2" }));
    expect(rowOf("Row 2").getAttribute("aria-selected")).toBe("true");
    expect(rowOf("Row 4").getAttribute("aria-selected")).toBe("true");
    fireEvent.click(within(rowOf("Row 4")).getByRole("button", { name: "Select Row 4" }));
    expect(rowOf("Row 4").getAttribute("aria-selected")).toBe("false");
    expect(rowOf("Row 2").getAttribute("aria-selected")).toBe("true");
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("a list without selectable shows no circle", () => {
    render(<Listing<Row> {...props({ selectable: false })} />);
    expect(document.querySelector(".empty-circle")).toBeNull();
    expect(document.querySelector("button.row-mark")).toBeNull();
  });
});

describe("a preset mark is drawn again when its own selection or the selection mode changes (decision 128)", () => {
  it("the selection going from none to some draws every row's mark once, so the circles appear", () => {
    render(<Listing<Row> {...props()} />);
    reset();
    fireEvent.click(rowOf("Row 4"));
    expect(rows.every((r) => (checks[r.name] ?? 0) > 0)).toBe(true);
  });

  it("with something selected, adding another row draws that row's mark only", () => {
    render(<Listing<Row> {...props()} />);
    fireEvent.click(rowOf("Row 4"));
    reset();
    fireEvent.click(rowOf("Row 2"), { ctrlKey: true });
    expect(checks["Row 2"]).toBeGreaterThan(0);
    expect(othersChecks(["Row 2"])).toBe(0);
  });

  it("presetMarkEqual reads the mode: a flip draws the mark again, another row's selection leaves it alone", () => {
    const pick = () => undefined;
    const base = { id: "r1", checked: false, name: "Row 1", pick, mode: false };
    const same = { ...base };
    expect(presetMarkEqual(base, same)).toBe(true);
    const modeOn = { ...base, mode: true };
    expect(presetMarkEqual(base, modeOn)).toBe(false);
    const checked = { ...base, checked: true };
    expect(presetMarkEqual(base, checked)).toBe(false);
    // Another row's selection changes nothing in this mark's own props.
    const still = { ...modeOn };
    expect(presetMarkEqual(modeOn, still)).toBe(true);
  });
});
