// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { get } from "../../lib/store";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { person, signedAs } from "../../test/adminFixtures";
import SignInAs from "./SignInAs";

const member = { id: "usr_9", email: "mateo@example.com", display_name: "Mateo Member", role: "member", locale: "en", status: "active" };

function renderSignInAs(onClose = () => undefined) {
  render(<SignInAs accountID="acc_1" person={person(member)} onClose={onClose} />);
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

describe("sign in as a tenant user", () => {
  it("says for how long, and that the admin session is kept, before the admin confirms", () => {
    renderSignInAs();
    expect(screen.getByRole("status").textContent).toBe(
      "You will be signed in as Mateo Member for one hour. Your admin session is kept and you can go back at any time.",
    );
  });

  it("an empty admin password is not sent", () => {
    const { calls } = stubFetch(() => ({ status: 200, body: {} }));
    renderSignInAs();
    fireEvent.click(screen.getByRole("button", { name: "Sign in as Mateo Member" }));
    expect(screen.getByText("Enter your password.")).toBeTruthy();
    expect(calls).toEqual([]);
  });

  it("the answer becomes the user's session, with its own token, and the browser goes home", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/accounts/acc_1/users/usr_9/sign-in") {
        return { status: 200, body: { user: member, csrf_token: "member-csrf" } };
      }
      if (c.url === "/api/v1/me") {
        return {
          status: 200,
          body: {
            user: member,
            account: { id: "acc_1", name: "Acme" },
            csrf_token: "member-csrf",
            impersonation: { ends_at: "2026-10-31T13:00:00Z" },
          },
        };
      }
      return { status: 500, body: { error: { code: "internal" } } };
    });
    renderSignInAs();
    fireEvent.change(document.getElementById("signin-as-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in as Mateo Member" }));
    await waitFor(() => expect(get(session)?.user.id).toBe("usr_9"));
    expect(get(session)?.csrf).toBe("member-csrf");
    expect(get(session)?.impersonation).toEqual({ endsAt: "2026-10-31T13:00:00Z" });
    expect(calls[0].body).toEqual({ admin_password: "admin-pass-1" });
    expect(window.location.pathname).toBe("/");
  });

  it("a user who must change the password first is not signed in, and the reason is said", async () => {
    stubFetch(() => fail(409, "conflict", { reason: "password_change_required" }));
    renderSignInAs();
    fireEvent.change(document.getElementById("signin-as-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in as Mateo Member" }));
    expect((await screen.findByRole("alert")).textContent).toContain("The user must choose a new password first.");
    expect(get(session)?.user.role).toBe("admin");
  });
});
