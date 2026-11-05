// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import type { ReactNode } from "react";
import { createQueryClient } from "../../lib/query";

// The dialog reads the query client (its cancel refreshes the lists), so every render has the provider.
const render = (ui: ReactNode) => rtlRender(<QueryClientProvider client={createQueryClient()}>{ui}</QueryClientProvider>);
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import NewTenant from "./NewTenant";
import { account, signedAs } from "../../test/adminFixtures";

function setField(id: string, value: string) {
  fireEvent.change(document.getElementById(id) as HTMLInputElement, { target: { value } });
}

// The admin's session carries the installation's two switches, as /me sends them (decision 41).
function adminWith(switches: { tenantAccess: boolean; mailConfigured: boolean }) {
  session.set({ ...signedAs("admin"), ...switches } as Session);
}

beforeEach(() => adminWith({ tenantAccess: true, mailConfigured: true }));
afterEach(() => session.set(null));

function fill() {
  setField("new-tenant-name", "Acme");
  setField("new-tenant-owner-email", "owen@example.com");
}

// The owner's ways in: the server takes email only with mail, and a link to an admin's session only with tenant access.
describe("the owner's ways in follow the installation's switches (decision 41)", () => {
  it("with mail only, offers the email invitation and no link", () => {
    adminWith({ tenantAccess: false, mailConfigured: true });
    render(<NewTenant onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.getByRole("radio", { name: "Send an email invitation" })).toBeTruthy();
    expect(screen.queryByRole("radio", { name: /link/i })).toBeNull();
  });

  it("with tenant access only, offers the link, selected, and no email", () => {
    adminWith({ tenantAccess: true, mailConfigured: false });
    render(<NewTenant onClose={vi.fn()} onCreated={vi.fn()} />);
    expect(screen.queryByRole("radio", { name: "Send an email invitation" })).toBeNull();
    expect((screen.getByRole("radio", { name: /link/i }) as HTMLInputElement).checked).toBe(true);
  });

  it("with neither, offers no way in, says so, and creates nothing", async () => {
    adminWith({ tenantAccess: false, mailConfigured: false });
    const { calls } = stubFetch(() => ({ status: 201, body: account() }));
    render(<NewTenant onClose={vi.fn()} onCreated={vi.fn()} />);
    fill();
    setField("new-tenant-limit", "5");
    expect(screen.queryByRole("radio")).toBeNull();
    expect(screen.getByRole("dialog").textContent).toContain("Mail is not set up");
    expect((screen.getByRole("button", { name: "Create tenant" }) as HTMLButtonElement).disabled).toBe(true);
    expect(calls).toEqual([]);
  });
});

describe("the new tenant dialog", () => {
  it("a click on the backdrop does not close it, since it holds a form (guard: maskClosable false)", async () => {
    stubFetch(() => ({ status: 200, body: {} }));
    const onClose = vi.fn();
    render(<NewTenant onClose={onClose} onCreated={vi.fn()} />);
    const mask = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.click(mask);
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("refuses an unreadable limit before any request (guard: limitBytes check)", async () => {
    const { calls } = stubFetch(() => ({ status: 201, body: account() }));
    render(<NewTenant onClose={vi.fn()} onCreated={vi.fn()} />);
    fill();
    setField("new-tenant-limit", "lots");
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Enter a storage limit");
    expect(calls).toEqual([]);
  });

  it("a double click creates the tenant once (guard: useGuard)", async () => {
    let posts = 0;
    stubFetch((c: Call) => {
      if (c.method === "POST") posts++;
      return { status: 201, body: { ...account(), owner: undefined } };
    });
    const onCreated = vi.fn();
    render(<NewTenant onClose={vi.fn()} onCreated={onCreated} />);
    fill();
    setField("new-tenant-limit", "5");
    const go = screen.getByRole("button", { name: "Create tenant" });
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
    expect(posts).toBe(1);
  });

  it("a refused create keeps the dialog and shows the code's text (guard: failureText)", async () => {
    stubFetch(() => fail(409, "conflict", { text: "SERVER-TEXT" }));
    const onCreated = vi.fn();
    render(<NewTenant onClose={vi.fn()} onCreated={onCreated} />);
    fill();
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    expect((await screen.findByRole("alert")).textContent).not.toBe("");
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(document.body.textContent).not.toContain("SERVER-TEXT");
    expect(onCreated).not.toHaveBeenCalled();
  });
});

describe("the new tenant dialog while the tenant is being created", () => {
  // The request may still create the tenant, so the dialog stays open until the answer comes (guard: busy on the dialog).
  // A busy form can be cancelled: it closes at once, and its request is aborted (busyCancel.test.tsx checks the abort).
  it("Cancel on the busy form closes it at once (guard: busy forms can be cancelled)", async () => {
    const { calls } = stubFetch((c: Call) => (c.method === "POST" ? new Promise(() => undefined) : { status: 200, body: {} }));
    const onClose = vi.fn();
    render(
      <App>
        <NewTenant onClose={onClose} onCreated={vi.fn()} />
      </App>,
    );
    fill();
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
