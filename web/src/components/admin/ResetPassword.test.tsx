// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { person, signedAs } from "../../test/adminFixtures";
import ResetPassword from "./ResetPassword";

const PASSWORD = "a brand new passphrase";
const URL_ONE_TIME = "http://127.0.0.1/reset#token=ONE-TIME-VALUE";

function renderReset(over: Partial<Parameters<typeof ResetPassword>[0]> = {}) {
  const props = {
    accountID: "acc_1",
    person: person({ id: "usr_9", email: "mateo@example.com", display_name: "Mateo Member", role: "member" }),
    mailConfigured: false,
    tenantAccess: true,
    onClose: vi.fn(),
    onDone: vi.fn(),
    onLink: vi.fn(),
    ...over,
  };
  render(<ResetPassword {...props} />);
  return props;
}

const path = (c: Call) => new URL(c.url, "http://x").pathname;

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("the ways to reset a password", () => {
  it("offers only the email when the installation has no tenant access", () => {
    renderReset({ mailConfigured: true, tenantAccess: false });
    expect(screen.getByLabelText("Send a reset email")).toBeTruthy();
    expect(screen.queryByLabelText("Make a one-time link")).toBeNull();
    expect(screen.queryByLabelText("Type a new password")).toBeNull();
  });

  it("offers the link and the typed password with tenant access, and no email without mail", () => {
    renderReset({ mailConfigured: false, tenantAccess: true });
    expect(screen.queryByLabelText("Send a reset email")).toBeNull();
    expect(screen.getByLabelText("Make a one-time link")).toBeTruthy();
    expect(screen.getByLabelText("Type a new password")).toBeTruthy();
  });

  it("says what will happen before the admin confirms, for each way", () => {
    renderReset({ mailConfigured: true, tenantAccess: true });
    expect(screen.getByRole("status").textContent).toContain("An email with a reset link is sent to mateo@example.com.");
    fireEvent.click(screen.getByLabelText("Type a new password"));
    expect(screen.getByRole("status").textContent).toContain("The user's current sessions end.");
    expect(screen.getByRole("status").textContent).toContain("The user must choose a new password at the next sign-in.");
    fireEvent.click(screen.getByLabelText("Make a one-time link"));
    expect(screen.getByRole("status").textContent).toContain("shown once");
  });
});

describe("resetting", () => {
  it("a typed password is sent with the admin's own password, and the parent is told it is done", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    const props = renderReset();
    fireEvent.click(screen.getByLabelText("Type a new password"));
    fireEvent.change(document.getElementById("reset-password") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.change(document.getElementById("reset-confirm") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.change(document.getElementById("reset-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(props.onDone).toHaveBeenCalledWith("The password was set. The user must choose a new password at the next sign-in."));
    const sent = calls.find((c) => path(c).endsWith("/password"));
    expect(sent?.method).toBe("POST");
    expect(sent?.body).toEqual({ password: PASSWORD, admin_password: "admin-pass-1" });
  });

  it("a typed password is not sent when the admin's password is left out", () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    renderReset();
    fireEvent.click(screen.getByLabelText("Type a new password"));
    fireEvent.change(document.getElementById("reset-password") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.change(document.getElementById("reset-confirm") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.click(screen.getByRole("button", { name: "Set password" }));
    expect(screen.getByText("Enter your password.")).toBeTruthy();
    expect(calls).toEqual([]);
  });

  it("a wrong admin password says so, and the dialog stays open", async () => {
    stubFetch(() => fail(403, "forbidden", {}));
    const props = renderReset();
    fireEvent.click(screen.getByLabelText("Type a new password"));
    fireEvent.change(document.getElementById("reset-password") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.change(document.getElementById("reset-confirm") as HTMLInputElement, { target: { value: PASSWORD } });
    fireEvent.change(document.getElementById("reset-admin") as HTMLInputElement, { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: "Set password" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Your admin password is not correct.");
    expect(props.onDone).not.toHaveBeenCalled();
  });

  it("a link is made once and handed to the parent, which shows it", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: { method: "link", url: URL_ONE_TIME } }));
    const props = renderReset();
    fireEvent.click(screen.getByLabelText("Make a one-time link"));
    fireEvent.change(document.getElementById("reset-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Make link" }));
    await waitFor(() => expect(props.onLink).toHaveBeenCalledWith(URL_ONE_TIME));
    expect(calls.filter((c) => path(c).endsWith("/password-reset"))).toHaveLength(1);
    expect(calls[0].body).toEqual({ method: "link", admin_password: "admin-pass-1" });
  });

  it("a link is not made when the admin's password is left out", () => {
    const { calls } = stubFetch(() => ({ status: 200, body: { method: "link", url: URL_ONE_TIME } }));
    const props = renderReset();
    fireEvent.click(screen.getByLabelText("Make a one-time link"));
    fireEvent.click(screen.getByRole("button", { name: "Make link" }));
    expect(calls.filter((c) => path(c).endsWith("/password-reset"))).toHaveLength(0);
    expect(props.onLink).not.toHaveBeenCalled();
  });

  it("the refused link says the tenant access is off", async () => {
    stubFetch(() => fail(403, "forbidden", { reason: "tenant_access_off" }));
    renderReset();
    fireEvent.click(screen.getByLabelText("Make a one-time link"));
    fireEvent.change(document.getElementById("reset-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Make link" }));
    expect((await screen.findByRole("alert")).textContent).toContain("This needs the tenant access setting.");
  });

  it("the email reports when it could not be sent", async () => {
    stubFetch(() => ({ status: 200, body: { method: "email", sent: false } }));
    const props = renderReset({ mailConfigured: true, tenantAccess: false });
    fireEvent.click(screen.getByRole("button", { name: "Send email" }));
    await waitFor(() => expect(props.onDone).toHaveBeenCalledWith("The email could not be sent. Try again later, or send a link instead."));
  });
});
