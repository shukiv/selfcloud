// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { stubFetch } from "../test/fetchStub";
import { setSignInInfo, SIGNED_OUT_PATH } from "../lib/outsideSignIn";
import { stubLocationAssign } from "../test/locationStub";
import ChangePasswordPage from "./ChangePasswordPage";

const member = { id: "usr_m", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en", status: "active" };

function signedIn(extra: Partial<Session> = {}): Session {
  return { user: member, account: null, csrf: "csrf", publicLinks: false, ...extra } as unknown as Session;
}

beforeEach(async () => {
  stubFetch(() => ({ status: 200, body: {} }));
  await setLang("en");
});
afterEach(() => session.set(null));

describe("the change-password page", () => {
  it("when the person is forced to change, it says so and offers a sign-out", () => {
    session.set(signedIn({ mustChange: true }));
    render(<ChangePasswordPage forced />);
    expect(screen.getByText("Choose a new password before you continue. Nothing else is open until you do.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Sign out" })).toBeTruthy();
    expect(document.getElementById("change-current")).toBeTruthy();
  });

  it("during an impersonation it says why, and shows no form", () => {
    session.set(signedIn({ impersonation: { endsAt: "2026-10-31T13:00:00Z" } }));
    render(<ChangePasswordPage />);
    expect(screen.getByRole("alert").textContent).toContain("You can't do this while signed in as another person.");
    expect(document.getElementById("change-current")).toBeNull();
    expect(screen.queryByRole("button", { name: "Change password" })).toBeNull();
  });
});

describe("the forced page's sign-out with an outside sign-in page (decision 185)", () => {
  let assign: ReturnType<typeof stubLocationAssign>;

  beforeEach(() => {
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/change-password");
    setSignInInfo(null);
    assign = stubLocationAssign();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    setSignInInfo(null);
    window.sessionStorage.clear();
  });

  it("goes to the signed-out URL when one is set", async () => {
    setSignInInfo({ externalUrl: "https://id.example.com/sign-in?next={next}", signedOutUrl: "https://id.example.com/signed-out", password: false });
    session.set(signedIn({ mustChange: true }));
    render(<ChangePasswordPage forced />);
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://id.example.com/signed-out"));
  });

  it("shows the signed-out page when only the outside sign-in page is set", async () => {
    setSignInInfo({ externalUrl: "https://id.example.com/sign-in?next={next}", signedOutUrl: "", password: false });
    session.set(signedIn({ mustChange: true }));
    render(<ChangePasswordPage forced />);
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(window.location.pathname).toBe(SIGNED_OUT_PATH));
    expect(assign).not.toHaveBeenCalled();
  });

  it("goes to /login when neither is set", async () => {
    session.set(signedIn({ mustChange: true }));
    render(<ChangePasswordPage forced />);
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(assign).not.toHaveBeenCalled();
  });
});
