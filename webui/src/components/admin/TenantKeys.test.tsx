// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import TenantKeys from "./TenantKeys";
import { key, signedAs } from "../../test/adminFixtures";

const SECRET = "scl_SECRET-KEY-VALUE-1234567890";

function renderKeys(qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })) {
  render(
    <QueryClientProvider client={qc}>
      <TenantKeys account="acc_1" />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

describe("API keys of a tenant", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderKeys();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("No API keys yet.")).toBeNull();
  });

  it("a new key is shown once in its dialog, and never enters the query cache (guard: no setQueryData)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...key({ id: "key_2", name: "CI" }), key: SECRET } };
      return { status: 200, body: { items: [key()] } };
    });
    const qc = renderKeys();
    await screen.findByText("CI");
    fireEvent.click(screen.getByRole("button", { name: "New key" }));
    fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: "Deploy" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    expect(((await screen.findByDisplayValue(SECRET)) as HTMLInputElement).id).toBe("new-key-once-value");
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data))).not.toContain(SECRET);
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    await waitFor(() => expect(screen.queryByDisplayValue(SECRET)).toBeNull());
    expect(document.body.textContent).not.toContain(SECRET);
  });

  it("a session change clears a shown key (guard: useOnSessionChange)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...key({ id: "key_2", name: "CI" }), key: SECRET } };
      return { status: 200, body: { items: [] } };
    });
    renderKeys();
    await screen.findByText("No API keys yet.");
    fireEvent.click(screen.getByRole("button", { name: "New key" }));
    fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: "CI" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    await screen.findByDisplayValue(SECRET);
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(screen.queryByDisplayValue(SECRET)).toBeNull());
  });

  it("revoke asks first, and a double click sends one request (the revoke button is disabled while the request is out; the inFlight ref is not proven here)", async () => {
    let revokes = 0;
    stubFetch((c: Call) => {
      if (c.method === "DELETE") {
        revokes++;
        return { status: 204 };
      }
      return { status: 200, body: { items: [key()] } };
    });
    renderKeys();
    await screen.findByText("CI");
    fireEvent.click(screen.getByRole("button", { name: /Revoke/ }));
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Revoke" });
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("was revoked"));
    expect(revokes).toBe(1);
  });

  it("a refused revoke keeps the dialog and shows the error by code (guard: revokeError)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "DELETE") return fail(403, "forbidden", { text: "SERVER-TEXT" });
      return { status: 200, body: { items: [key()] } };
    });
    renderKeys();
    await screen.findByText("CI");
    fireEvent.click(screen.getByRole("button", { name: /Revoke/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("not allowed"));
    expect(document.body.textContent).not.toContain("SERVER-TEXT");
  });

  // A revoke that is answered after the person changed says nothing to the next person (the notice is that of the
  // person who asked, and the dialog is theirs to close).
  it("a revoke answered after the person changed shows no notice to the next person (guard: changed check after the revoke)", async () => {
    let release!: (a: { status: number }) => void;
    const held = new Promise<{ status: number }>((resolve) => (release = resolve));
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return held;
      return { status: 200, body: { items: [key()] } };
    });
    renderKeys();
    await screen.findByText("CI");
    fireEvent.click(screen.getByRole("button", { name: /Revoke/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    act(() => session.set(signedAs("admin", "admin_2")));
    release({ status: 204 });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(/was revoked/)).toBeNull();
  });

  // The form holds what the admin typed. A change of person closes it, so the name and the form go with the person who typed them.
  it("a session change closes the new key form with what was typed (guard: the form closes on a session change)", async () => {
    stubFetch((c: Call) => (c.method === "GET" ? { status: 200, body: { items: [] } } : { status: 201, body: key() }));
    renderKeys();
    await screen.findByText("No API keys yet.");
    fireEvent.click(screen.getByRole("button", { name: "New key" }));
    fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: "typed by Ada" } });
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(document.getElementById("new-key-name")).toBeNull());
  });
});

describe("the new key dialog while a key is being created", () => {
  // The request may still create the key, so the dialog stays open until the answer comes (guard: busy on the dialog).
  it("Cancel, Escape and the close button do nothing while the request is out (guard: busy)", async () => {
    let answer!: (a: Answer) => void;
    stubFetch((c: Call) =>
      c.method === "POST" ? new Promise<Answer>((r) => (answer = r)) : { status: 200, body: { items: [] } },
    );
    renderKeys();
    await screen.findByText("No API keys yet.");
    fireEvent.click(screen.getByRole("button", { name: "New key" }));
    fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: "Deploy" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    await waitFor(() => expect(answer).toBeDefined());
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.keyDown(document.querySelector(".ant-modal") as HTMLElement, { key: "Escape" });
    const close = document.querySelector(".ant-modal-close");
    if (close) fireEvent.click(close);
    expect(document.getElementById("new-key-name")).not.toBeNull();
    answer({ status: 201, body: { ...key({ id: "key_2", name: "Deploy" }), key: SECRET } });
    expect(((await screen.findByDisplayValue(SECRET)) as HTMLInputElement).id).toBe("new-key-once-value");
  });
});
