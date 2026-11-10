// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadSearchText } from "../lib/search/text";
import { stubFetch, type Answer } from "../test/fetchStub";
import SearchPage from "./SearchPage";

// The first load of the page's strings fails (a chunk that cannot be fetched); the next one works.
vi.mock("../lib/search/text", () => {
  let calls = 0;
  return {
    loadSearchText: vi.fn(async () => {
      calls += 1;
      if (calls === 1) throw new Error("chunk");
      // The retry loads the page's strings, as the real loader does.
      const { addCatalogue } = await import("../lib/i18n");
      addCatalogue("en", (await import("../i18n/search/en.json")).default);
    }),
  };
});

beforeEach(() => {
  stubFetch((c) => (c.url.startsWith("/api/v1/label-fields") ? { status: 200, body: { items: [] } } : ({ status: 200, body: { items: [] } } as Answer)));
});

describe("the search page when its strings fail to load", () => {
  it("says so, offers Try again, and the page works after a second try", async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={["/search"]}>
          <Routes>
            <Route path="/search" element={<SearchPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Something went wrong");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(loadSearchText).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Type a word, or choose a filter.")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
