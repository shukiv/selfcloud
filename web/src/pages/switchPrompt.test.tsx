// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { ThemeProvider } from "../theme/theme";
import { fail, stubFetch } from "../test/fetchStub";
import VerifyPage from "./VerifyPage";
import ResetPage from "./ResetPage";

// fu2 (decision 74): the verify and reset pages ask a browser signed in as another person before they switch,
// with the same prompt the sign-in link shows, and send confirm only when the person answers yes.
const ask = fail(409, "conflict", {
  reason: "signed_in_as_other",
  current: { name: "Ann Current", email: "ann@example.com" },
  target: { name: "", email: "ben@example.com" },
});

beforeEach(async () => {
  await setLang("en");
  window.location.hash = "#token=tok_test_switch";
});
afterEach(() => {
  session.set(null);
  window.location.hash = "";
});

function fill(prefix: string) {
  const password = document.getElementById(`${prefix}-password`) as HTMLInputElement;
  const confirm = document.getElementById(`${prefix}-confirm`) as HTMLInputElement;
  fireEvent.change(password, { target: { value: "correct horse battery" } });
  fireEvent.change(confirm, { target: { value: "correct horse battery" } });
}

describe("the verify page asks before it switches", () => {
  it("shows the sign-in prompt, and sends confirm only when the person accepts", async () => {
    const { calls } = stubFetch((c) => (c.body as { confirm?: boolean }).confirm ? fail(500, "internal") : ask);
    render(<ThemeProvider><VerifyPage /></ThemeProvider>);
    fill("verify");
    fireEvent.click(screen.getByRole("button", { name: "Set password and sign in" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Sign in as another user?")).toBeTruthy();
    expect(within(dialog).getAllByText(/Ann Current/).length).toBeGreaterThan(0);
    expect(calls).toHaveLength(1);
    expect(calls[0]?.body).not.toHaveProperty("confirm");

    fireEvent.click(within(dialog).getByRole("button", { name: /Switch to ben@example.com/ }));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.body).toMatchObject({ token: "tok_test_switch", confirm: true });
  });
});

describe("the reset page asks before it switches", () => {
  it("shows the sign-in prompt, and sends confirm only when the person accepts", async () => {
    const { calls } = stubFetch((c) => (c.body as { confirm?: boolean }).confirm ? fail(500, "internal") : ask);
    render(<ThemeProvider><ResetPage /></ThemeProvider>);
    fill("reset");
    fireEvent.click(screen.getByRole("button", { name: "Save and sign in" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Sign in as another user?")).toBeTruthy();
    expect(calls).toHaveLength(1);
    expect(calls[0]?.body).not.toHaveProperty("confirm");

    fireEvent.click(within(dialog).getByRole("button", { name: /Switch to ben@example.com/ }));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.body).toMatchObject({ token: "tok_test_switch", confirm: true });
  });
});
