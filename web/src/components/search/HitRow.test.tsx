// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { SearchHit } from "../../lib/search/api";
import HitRow from "./HitRow";

const hit = (over: Partial<SearchHit>): SearchHit => ({
  id: "nod_x",
  folder_id: "fld_a",
  parent_id: null,
  name: "Item",
  kind: "file",
  size: 10,
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-02T00:00:00Z",
  no_download: false,
  editors_can_share: false,
  ...over,
});

const row = (h: SearchHit) =>
  render(
    <MemoryRouter>
      <ul>
        <HitRow hit={h} />
      </ul>
    </MemoryRouter>,
  );

describe("a result row", () => {
  it("links a file to its own address", () => {
    row(hit({ id: "nod_f", name: "Plan.txt" }));
    expect(screen.getByRole("link", { name: "Plan.txt" }).getAttribute("href")).toBe("/n/nod_f");
  });

  it("links a shortcut the caller reaches to its target", () => {
    row(hit({ id: "nod_sc", name: "Link", kind: "shortcut", target: { id: "nod_t", kind: "file" } }));
    expect(screen.getByRole("link", { name: "Link" }).getAttribute("href")).toBe("/n/nod_t");
  });

  it("links an unavailable shortcut to its own address, as a listing's access request is made from it", () => {
    row(hit({ id: "nod_sc", name: "Gone link", kind: "shortcut", unavailable: true }));
    expect(screen.getByRole("link", { name: "Gone link" }).getAttribute("href")).toBe("/n/nod_sc");
  });
});
