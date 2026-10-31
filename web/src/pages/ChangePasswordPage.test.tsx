// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { stubFetch } from "../test/fetchStub";
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
