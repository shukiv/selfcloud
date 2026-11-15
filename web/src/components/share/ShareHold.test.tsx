// SPDX-License-Identifier: AGPL-3.0-or-later

// A share opened while signed out (decision 82): its files are taken into the page's memory at once, before the
// person is sent to sign in, because the worker may be stopped or the share may expire during a long sign-in.

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setSignInInfo } from "../../lib/outsideSignIn";
import { stubLocationAssign } from "../../test/locationStub";

const takeShare = vi.fn(async (_id: string, _opts?: { signedOut?: boolean }) => [] as File[]);
vi.mock("../../lib/pwa/share", () => ({
  takeShare: (id: string, opts?: { signedOut?: boolean }) => takeShare(id, opts),
}));

import ShareHold from "./ShareHold";

function Where() {
  const loc = useLocation();
  return <p data-testid="where">{loc.pathname}</p>;
}

beforeEach(() => {
  takeShare.mockClear();
  setSignInInfo(null);
  window.sessionStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  setSignInInfo(null);
  window.sessionStorage.clear();
});

describe("ShareHold", () => {
  it("takes the share's files at once, then goes to sign-in with the share's address", async () => {
    render(
      <MemoryRouter initialEntries={["/share?id=h1"]}>
        <Routes>
          <Route path="/share" element={<ShareHold id="h1" from="/share?id=h1" />} />
          <Route path="/login" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    );
    // The take is made as a signed-out take, so the page keeps the files for the five-minute time-box (decision 82).
    await waitFor(() => expect(takeShare).toHaveBeenCalledWith("h1", { signedOut: true }));
    expect((await screen.findByTestId("where")).textContent).toBe("/login");
  });

  it("with an outside sign-in page, takes the files first, then assigns the outside page (decision 204)", async () => {
    setSignInInfo({ externalUrl: "https://id.example.com/sign-in?next={next}", signedOutUrl: "", password: false });
    const assign = stubLocationAssign();
    render(
      <MemoryRouter initialEntries={["/share?id=h1"]}>
        <Routes>
          <Route path="/share" element={<ShareHold id="h1" from="/share?id=h1" />} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2Fshare%3Fid%3Dh1"));
    expect(takeShare).toHaveBeenCalledWith("h1", { signedOut: true });
    // The take is given its answer before the page leaves for the outside sign-in page.
    expect(takeShare.mock.invocationCallOrder[0]).toBeLessThan(assign.mock.invocationCallOrder[0]);
  });
});