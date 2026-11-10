// SPDX-License-Identifier: AGPL-3.0-or-later

import { screen, waitFor, within } from "@testing-library/react";
import { expect } from "vitest";

// expectEmptyTable checks an empty preset list (the owner's rule of 2026-11-09, decision 114). The table and its header row
// stay, with the column titles; inside the table's body sit antd's simple Empty, the bold title, the muted description (from
// the admin catalogue, which arrives after the page) and, when the page has one, its create action: a primary button with a
// + icon. It returns that button, or null for a list with no action.
export async function expectEmptyTable(
  region: string,
  want: { headers: string[]; title: string; description: string; action?: string },
): Promise<HTMLElement | null> {
  const r = await screen.findByRole("region", { name: region });
  // Listing draws the phone layout until antd's breakpoint is known, so the desktop headers come a render later: wait.
  await waitFor(() => {
    const t = r.querySelector("table") as HTMLElement;
    expect(t).not.toBeNull();
    expect(within(t).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(want.headers);
  });
  const table = r.querySelector("table") as HTMLElement;
  const body = table.querySelector("tbody") as HTMLElement;
  await within(body).findByText(want.description);
  expect(body.querySelector(".ant-empty.ant-empty-normal .ant-empty-image")).not.toBeNull();
  expect(body.querySelector(".empty-title")?.textContent).toBe(want.title);
  expect(body.querySelector(".empty-desc")?.textContent).toBe(want.description);
  if (!want.action) {
    expect(within(body).queryByRole("button")).toBeNull();
    return null;
  }
  const button = within(body).getByRole("button", { name: want.action });
  expect(button.className).toContain("ant-btn-primary");
  expect(button.querySelector("svg.lucide-plus")).not.toBeNull();
  return button;
}
