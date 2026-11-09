// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import TenantKeys from "./TenantKeys";
import { key, signedAs } from "../../test/adminFixtures";

const SECRET = "scl_SECRET-KEY-VALUE-1234567890";

function renderKeys(qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })) {
  render(
    <App>
      <QueryClientProvider client={qc}>
        <TenantKeys account="acc_1" />
      </QueryClientProvider>
    </App>,
  );
  return qc;
}

// These tests are about a desktop: the Revoke icon shows inline there, and below md it moves into the ⋮ menu (design-tl 4).
const desktop = (query: string) => {
  const m = /min-width: (\d+)px/.exec(query);
  return { matches: m ? 1200 >= Number(m[1]) : false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
};
let savedMatchMedia: typeof window.matchMedia;
beforeEach(() => {
  session.set(signedAs("admin"));
  savedMatchMedia = window.matchMedia;
  window.matchMedia = desktop as unknown as typeof window.matchMedia;
});
afterEach(() => {
  session.set(null);
  window.matchMedia = savedMatchMedia;
  window.history.replaceState(null, "", "/");
});

// tl1: the keys are loaded whole, so the table sorts them on the client by name and by creation time (design-tl section 5).
describe("the keys table sorts on the client", () => {
  it("a header click on Name orders the rows by name, and Created by creation time", async () => {
    stubFetch((c: Call) => {
      if (c.method === "GET") {
        return {
          status: 200,
          body: { items: [key({ id: "key_z", name: "Zeta", created_at: "2026-01-01T00:00:00Z" }), key({ id: "key_a", name: "Alpha", created_at: "2026-02-01T00:00:00Z" })] },
        };
      }
      return { status: 200, body: {} };
    });
    renderKeys();
    await screen.findByText("Zeta");
    const rowNames = () => Array.from(document.querySelectorAll("tbody tr[aria-label]")).map((r) => r.getAttribute("aria-label"));
    expect(rowNames()).toEqual(["Zeta", "Alpha"]);
    fireEvent.click(within(screen.getByRole("region", { name: "API keys" })).getByRole("button", { name: "Name" }));
    expect(rowNames()).toEqual(["Alpha", "Zeta"]);
    expect(screen.getByRole("columnheader", { name: /Name/ }).getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(screen.getByRole("region", { name: "API keys" })).getByRole("button", { name: "Created" }));
    expect(rowNames()).toEqual(["Zeta", "Alpha"]);
    expect(screen.getByRole("columnheader", { name: /Created/ }).getAttribute("aria-sort")).toBe("ascending");
  });
});

// Making a key from an admin's session needs the admin's own password (decision 84). The prompt is the sign-in-as dialog,
// which opens on Create key; the key is sent only once the password is confirmed.
function fillKeyAndCreate(name = "CI") {
  fireEvent.click(screen.getByRole("button", { name: "New key" }));
  fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: "Create key" }));
}

function confirmWith(password: string) {
  fireEvent.change(document.getElementById("new-key-admin") as HTMLInputElement, { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
}

describe("a new key needs the admin's password", () => {
  it("asks for the password before anything is sent, then sends it with the key", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...key({ id: "key_2", name: "CI" }), key: SECRET } };
      return { status: 200, body: { items: [] } };
    });
    renderKeys();
    await screen.findByText("No API keys yet.");
    // The empty list is antd's Empty, compact, with its words (decision 111).
    expect(document.querySelector(".ant-empty.ant-empty-normal .ant-empty-description")?.textContent).toBe("No API keys yet.");
    fillKeyAndCreate();
    expect(document.getElementById("new-key-admin")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    confirmWith("admin-pass-1");
    await screen.findByDisplayValue(SECRET);
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ name: "CI", admin_password: "admin-pass-1" });
  });

  it("a wrong password is refused in the prompt, and no key is shown", async () => {
    stubFetch((c: Call) => (c.method === "POST" ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderKeys();
    await screen.findByText("No API keys yet.");
    fillKeyAndCreate();
    confirmWith("not the admin password");
    expect(await screen.findByText("Your admin password is not correct.")).toBeTruthy();
    expect(document.getElementById("new-key-once-value")).toBeNull();
  });
});

describe("the new key dialog's name is not an ambiguous Name", () => {
  it("labels the field Key name", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderKeys();
    fireEvent.click(await screen.findByRole("button", { name: "New key" }));
    expect(document.querySelector('label[for="new-key-name"]')?.textContent).toBe("Key name");
  });
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
    confirmWith("admin-pass-1");
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
    confirmWith("admin-pass-1");
    await screen.findByDisplayValue(SECRET);
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(screen.queryByDisplayValue(SECRET)).toBeNull());
  });

  it("a revoke that never answers: Cancel closes the dialog and aborts the request (fu4, item 2)", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return new Promise<Answer>(() => undefined);
      return { status: 200, body: { items: [key()] } };
    });
    renderKeys();
    await screen.findByText("CI");
    fireEvent.click(screen.getByRole("button", { name: /Revoke/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find((c) => c.method === "DELETE")?.signal?.aborted).toBe(true);
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
  // A busy form can be cancelled: it closes at once, and its request is aborted (busyCancel.test.tsx checks the abort).
  it("Cancel on the busy form closes it at once (guard: busy forms can be cancelled)", async () => {
    const { calls } = stubFetch((c: Call) => (c.method === "POST" ? new Promise<Answer>(() => undefined) : { status: 200, body: { items: [] } }));
    renderKeys();
    await screen.findByText("No API keys yet.");
    fireEvent.click(screen.getByRole("button", { name: "New key" }));
    fireEvent.change(document.getElementById("new-key-name") as HTMLInputElement, { target: { value: "Deploy" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    confirmWith("admin-pass-1");
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    // The key form is the first dialog; the password prompt is on top of it. Cancel on the key form closes both at once.
    fireEvent.click(within(screen.getAllByRole("dialog")[0]).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.getElementById("new-key-name")).toBeNull());
    expect(calls.find((c) => c.method === "POST")?.signal?.aborted).toBe(true);
  });
});
