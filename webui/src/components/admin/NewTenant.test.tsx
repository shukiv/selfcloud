// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import NewTenant from "./NewTenant";
import { account, signedAs } from "../../test/adminFixtures";

function setField(id: string, value: string) {
  fireEvent.change(document.getElementById(id) as HTMLInputElement, { target: { value } });
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

function fill() {
  setField("new-tenant-name", "Acme");
  setField("new-tenant-owner-email", "owen@example.com");
}

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
  it("Cancel, Escape and the close button do nothing while the request is out (guard: busy)", async () => {
    let answer!: (a: { status: number; body?: unknown }) => void;
    stubFetch((c: Call) =>
      c.method === "POST" ? new Promise((r) => (answer = r)) : { status: 200, body: {} },
    );
    const onClose = vi.fn();
    const onCreated = vi.fn();
    render(<NewTenant onClose={onClose} onCreated={onCreated} />);
    fill();
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    await waitFor(() => expect(answer).toBeDefined());
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.keyDown(document.querySelector(".ant-modal") as HTMLElement, { key: "Escape" });
    const close = document.querySelector(".ant-modal-close");
    if (close) fireEvent.click(close);
    expect(onClose).not.toHaveBeenCalled();
    answer({ status: 201, body: { ...account(), owner: undefined } });
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
  });
});
