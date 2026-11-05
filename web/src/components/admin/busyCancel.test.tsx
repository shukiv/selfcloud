// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import NewKey from "./NewKey";
import NewTenant from "./NewTenant";

// The admin's session, with mail configured so the new tenant's owner gets an email invitation (no password needed).
function signedAs(): Session {
  return {
    user: { id: "usr_admin", email: "admin@example.com", display_name: "Admin", role: "admin", locale: "en", status: "active" },
    csrf: "csrf-admin",
    publicLinks: false,
    mailConfigured: true,
  } as unknown as Session;
}

// hanging stubs a fetch that never answers, and records the signal of each request. An abort rejects it, as the browser does.
function hanging(): AbortSignal[] {
  const signals: AbortSignal[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_path: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init.signal as AbortSignal;
          signals.push(signal);
          signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    ),
  );
  return signals;
}

// The app wraps every page in antd's App, which gives the message the cancelled notice uses (main.tsx does the same).
const wrap = (node: ReactNode) => (
  <App>
    <QueryClientProvider client={createQueryClient()}>{node}</QueryClientProvider>
  </App>
);

beforeEach(() => session.set(signedAs()));
afterEach(() => {
  session.set(null);
  vi.unstubAllGlobals();
});

// Each dialog that makes a write can be cancelled while the write is out (guard: busy dialogs cancel their request).
describe("cancelling a busy admin dialog", () => {
  it("New key: Cancel aborts the key request and closes the dialog", async () => {
    const signals = hanging();
    const onClose = vi.fn();
    render(wrap(<NewKey account="acc_1" onClose={onClose} onCreated={vi.fn()} />));
    fireEvent.change(screen.getByLabelText("Key name"), { target: { value: "ci" } });
    fireEvent.click(screen.getByRole("button", { name: "Create key" }));
    // The key is sent once the admin's password is confirmed in the prompt on top (decision 84).
    fireEvent.change(document.getElementById("new-key-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    // Cancel on the key form, the first dialog, aborts the request and closes both dialogs.
    fireEvent.click(within(screen.getAllByRole("dialog")[0]).getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("New tenant: Cancel aborts the tenant request and closes the dialog", async () => {
    const signals = hanging();
    const onClose = vi.fn();
    render(wrap(<NewTenant onClose={onClose} onCreated={vi.fn()} />));
    fireEvent.change(document.getElementById("new-tenant-name") as HTMLElement, { target: { value: "Acme" } });
    fireEvent.change(document.getElementById("new-tenant-limit") as HTMLElement, { target: { value: "5" } });
    fireEvent.change(document.getElementById("new-tenant-owner-email") as HTMLElement, { target: { value: "owen@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
