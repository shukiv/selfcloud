// SPDX-License-Identifier: AGPL-3.0-or-later

// A share opened while signed out (decision 82): its files are taken into the page's memory at once, before the
// person is sent to sign in, because the worker may be stopped or the share may expire during a long sign-in.

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const takeShare = vi.fn(async (_id: string, _opts?: { signedOut?: boolean }) => [] as File[]);
vi.mock("../../lib/pwa/share", () => ({
  takeShare: (id: string, opts?: { signedOut?: boolean }) => takeShare(id, opts),
}));

import ShareHold from "./ShareHold";

function Where() {
  const loc = useLocation();
  return <p data-testid="where">{loc.pathname}</p>;
}

describe("ShareHold", () => {
  it("takes the share's files at once, then goes to sign-in with the share's address", async () => {
    render(
      <MemoryRouter initialEntries={["/share?id=h1"]}>
        <Routes>
          <Route path="/share" element={<ShareHold id="h1" to="/sign-in?next=%2Fshare%3Fid%3Dh1" />} />
          <Route path="/sign-in" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    );
    // The take is made as a signed-out take, so the page keeps the files for the five-minute time-box (decision 82).
    await waitFor(() => expect(takeShare).toHaveBeenCalledWith("h1", { signedOut: true }));
    expect((await screen.findByTestId("where")).textContent).toBe("/sign-in");
  });
});
