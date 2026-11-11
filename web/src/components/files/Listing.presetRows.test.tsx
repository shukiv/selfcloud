// SPDX-License-Identifier: AGPL-3.0-or-later

// A preset list (Starred, Trash, Search, the Manage and admin tables) draws its rows as My files does: a press is one update,
// and a row's checkbox, icons and ⋮ button are drawn again only when that row's own state changes (decision 128).
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Listing, { type ItemListingProps, type ListAction } from "./Listing";
import { desktopViewport } from "../../test/viewport";

// buttons counts the renders of each icon button by its label, and checks the renders of each row's checkbox by its name.
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
vi.mock("antd", async (orig) => {
  const real = await orig<typeof import("antd")>();
  const { createElement } = await import("react");
  const Checkbox = (p: Record<string, unknown>) => {
    const name = String(p["aria-label"] ?? "");
    checks[name] = (checks[name] ?? 0) + 1;
    return createElement(real.Checkbox, p as never);
  };
  return { ...real, Checkbox };
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
const othersSum = (counts: Record<string, number>, except: string[]) =>
  rows.filter((r) => !except.includes(r.name)).reduce((s, r) => s + sumFor(counts, r.name), 0);

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
});
afterEach(() => {
  undoView();
  reset();
});

describe("a preset list draws a row again only when its own state changes (decision 128)", () => {
  it("a click that selects a row draws that row's checkbox, and no other row's checkbox or buttons", () => {
    render(<Listing<Row> {...props()} />);
    reset();
    fireEvent.click(rowOf("Row 4"));
    expect(rowOf("Row 4").getAttribute("aria-selected")).toBe("true");
    expect(checks["Select Row 4"]).toBeGreaterThan(0);
    expect(othersSum(checks, ["Row 4"])).toBe(0);
    expect(Object.keys(buttons)).toEqual([]);
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
    expect(othersSum(checks, ["Row 2"])).toBe(0);
  });

  it("a hover on a row draws no row's buttons again", () => {
    render(<Listing<Row> {...props()} />);
    reset();
    fireEvent.pointerEnter(rowOf("Row 6"), { pointerType: "mouse" });
    fireEvent.pointerLeave(rowOf("Row 6"), { pointerType: "mouse" });
    expect(Object.keys(buttons)).toEqual([]);
  });

  it("a page render with new callbacks and the same data draws no row's buttons or checkbox again", () => {
    const { rerender } = render(<Listing<Row> {...props()} />);
    reset();
    rerender(<Listing<Row> {...props()} />);
    expect(Object.keys(buttons)).toEqual([]);
    expect(Object.keys(checks)).toEqual([]);
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
