// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import SearchPage from "./SearchPage";

// The page's own strings never arrive in this file, so the page is seen before (and without) its catalogue.
vi.mock("../lib/search/text", () => ({
  loadSearchText: vi.fn(() => new Promise<void>(() => undefined)),
}));

describe("the search page before its strings load", () => {
  it("its heading is a word of the main catalogue, never a raw key", () => {
    render(
      <MemoryRouter initialEntries={["/search"]}>
        <Routes>
          <Route path="/search" element={<SearchPage />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Search");
    expect(document.body.textContent ?? "").not.toMatch(/search\.[a-z]/);
  });
});
