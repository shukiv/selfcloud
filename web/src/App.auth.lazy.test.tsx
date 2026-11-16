// SPDX-License-Identifier: AGPL-3.0-or-later

// The auth pages are lazy chunks (decision 198): the signed-in first load does not carry their antd Input, Radio and
// Typography. This file runs the lazy path through the gate, and the idle warm that keeps the first open quick. The
// shell and the files page are stubs, as in App.roles.test.tsx: this file is about the gate, not about what they draw.

import { act, render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Direction, Gate } from "./App";
import { session, type Session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";

vi.mock("./components/Shell", async () => (await import("./test/stubPage")).stubShell());
vi.mock("./pages/FilesPage", async () => (await import("./test/stubPage")).stubPage("files"));

function renderAt(path: string) {
  const router = createMemoryRouter([{ path: "*", element: <Gate /> }], { initialEntries: [path] });
  return render(
    <ThemeProvider>
      <Direction>
        <RouterProvider router={router} />
      </Direction>
    </ThemeProvider>,
  );
}

const member = {
  user: { id: "usr_m", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en", status: "active" },
  account: null,
  csrf: "csrf",
  publicLinks: false,
} as unknown as Session;

beforeEach(async () => {
  window.localStorage.clear();
  session.set(null);
  await setLang("en");
});

afterEach(() => {
  session.set(null);
  vi.unstubAllGlobals();
});

describe("the auth pages load lazily", () => {
  // This one runs first: once a lazy page has resolved in this file, React keeps its payload, and the placeholder
  // below is no longer observable.
  it("a signed-out visitor gets the loading placeholder the boot shows, then the sign-in form", async () => {
    renderAt("/login");
    expect(screen.queryByLabelText("Email")).toBeNull();
    expect(screen.getByRole("status")).toBeTruthy();
    expect(await screen.findByLabelText("Email")).toBeTruthy();
    expect(screen.getByLabelText("Password")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("the signed-in shell warms them on idle, so a later sign-out opens the form with no wait", async () => {
    const idle: IdleRequestCallback[] = [];
    vi.stubGlobal("requestIdleCallback", (cb: IdleRequestCallback) => {
      idle.push(cb);
      return idle.length;
    });
    session.set(member);
    const view = renderAt("/files");
    await screen.findByText("stub files");
    // The first screen is drawn; the warm is scheduled for the idle time after it, not before.
    expect(idle.length).toBeGreaterThan(0);
    await act(async () => {
      for (const cb of idle) cb({ didTimeout: false, timeRemaining: () => 10 });
    });
    view.unmount();
    session.set(null);
    renderAt("/login");
    expect(await screen.findByLabelText("Email")).toBeTruthy();
    expect(screen.getByLabelText("Password")).toBeTruthy();
  });

  it("the device confirmation opens from its link with its button", async () => {
    window.history.replaceState(null, "", "/confirm-device#token=abc");
    renderAt("/confirm-device");
    expect(await screen.findByRole("heading", { name: "Confirm this browser" })).toBeTruthy();
    expect(await screen.findByRole("button", { name: "Confirm and sign in" })).toBeTruthy();
    window.history.replaceState(null, "", "/");
  });

  // Every auth page opens through the same lazy path; each shows its title even when its link is missing or spent.
  it.each([
    ["/forgot", "Forgot password"],
    ["/reset", "Choose a new password"],
    ["/verify", "Set your password"],
    ["/sign-in", "Signing you in"],
    ["/email-change", "Confirm your new email address"],
  ])("%s opens with its title", async (path, title) => {
    const view = renderAt(path);
    expect(await screen.findByRole("heading", { name: title })).toBeTruthy();
    view.unmount();
  });
});
