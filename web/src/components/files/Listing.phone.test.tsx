// SPDX-License-Identifier: AGPL-3.0-or-later

// The two props a page adds to a preset list for tl5 (decision 138): phone forces the phone layout whatever the window is (a narrow
// side panel), and rowTestID gives each row its test id, which the e2e checks read.
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import Listing, { type ItemListingProps } from "./Listing";
import { desktopViewport } from "../../test/viewport";

type Row = { id: string; name: string };
const rows: Row[] = [
  { id: "a", name: "Alpha" },
  { id: "b", name: "Beta" },
];
const columns: ItemListingProps<Row>["columns"] = [
  { key: "name", title: "Name", render: (r) => r.name },
  { key: "when", title: "When", secondary: true, ariaSort: "descending", render: () => "today" },
];

function list(over: Partial<ItemListingProps<Row>> = {}) {
  return render(<Listing<Row> label="Things" items={rows} rowLabel={(r) => r.name} columns={columns} empty={{ title: "Nothing here" }} {...over} />);
}

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
});
afterEach(() => {
  undoView();
});

describe("a preset list's phone layout is forced by the page (tl5)", () => {
  it("on a desktop, a secondary column is a column", () => {
    list();
    expect(screen.getByRole("columnheader", { name: "When" })).toBeTruthy();
  });

  it("with phone, the secondary column is the name's second line, at any width", () => {
    list({ phone: true });
    expect(screen.queryByRole("columnheader", { name: "When" })).toBeNull();
    expect(screen.getByRole("row", { name: "Alpha" }).querySelector(".row-second")?.textContent).toContain("today");
  });
});

describe("a preset list's rows take a test id from the page (tl5)", () => {
  it("gives each row the id the page names", () => {
    list({ rowTestID: (r) => `row-${r.id}` });
    expect(screen.getByTestId("row-a").getAttribute("role")).toBe("row");
    expect(screen.getByTestId("row-b").getAttribute("aria-label")).toBe("Beta");
  });

  it("gives no test id when the page names none", () => {
    list();
    expect(document.querySelector("[data-testid]")).toBeNull();
  });
});
