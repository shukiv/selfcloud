// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { signedAs, usage } from "../../test/adminFixtures";
import DeleteTenant from "./DeleteTenant";

afterEach(() => {
  session.set(null);
  vi.useRealTimers();
});

// The deletion request that never answers is cancelled at once: the dialog closes and the request is aborted (fu4).
describe("cancelling the tenant deletion while its request is out", () => {
  it("Cancel closes the dialog and aborts the request that asks for the deletion", async () => {
    session.set(signedAs("admin", "admin_1"));
    const onClose = vi.fn();
    const { calls } = stubFetch((c: Call): Answer | Promise<Answer> => {
      if (c.method === "DELETE") return new Promise<Answer>(() => undefined);
      return { status: 200, body: usage() };
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <App>
        <QueryClientProvider client={qc}>
          <DeleteTenant id="acc_1" name="Acme Ltd" onClose={onClose} onDeleted={vi.fn()} />
        </QueryClientProvider>
      </App>,
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Acme Ltd" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /Acme Ltd/ }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(calls.find((c) => c.method === "DELETE")?.signal?.aborted).toBe(true);
  });

  // The deletion may have started, so the tenant list and this tenant are read again.
  it("a cancel after the deletion was sent reads the tenant list and the tenant again (fu4)", async () => {
    session.set(signedAs("admin", "admin_1"));
    const onClose = vi.fn();
    const { calls: sent } = stubFetch((c: Call): Answer | Promise<Answer> => {
      if (c.method === "DELETE") return new Promise<Answer>(() => undefined);
      return { status: 200, body: usage() };
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    render(
      <App>
        <QueryClientProvider client={qc}>
          <DeleteTenant id="acc_1" name="Acme Ltd" onClose={onClose} onDeleted={vi.fn()} />
        </QueryClientProvider>
      </App>,
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Acme Ltd" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /Acme Ltd/ }));
    await waitFor(() => expect(sent.some((c) => c.method === "DELETE")).toBe(true));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(invalidate.mock.calls.some(([arg]) => JSON.stringify(arg).includes("admin-tenant"))).toBe(true);
  });
});

// A sign-out while the deletion job is polled: the poll goes quiet, as a stopped one, and nothing is reported.
describe("a sign-out while the deletion job is polled", () => {
  it("asks no more and reports nothing: no error, no deletion shown", async () => {
    vi.useFakeTimers();
    session.set(signedAs("admin", "admin_1"));
    const onDeleted = vi.fn();
    const { calls } = stubFetch((c: Call): Answer => {
      if (c.method === "DELETE") {
        return { status: 202, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "queued", created_at: "", finished_at: null } };
      }
      if (c.url.includes("/api/v1/jobs/")) {
        return { status: 200, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "running", created_at: "", finished_at: null } };
      }
      return { status: 200, body: usage() };
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <App>
        <QueryClientProvider client={qc}>
          <DeleteTenant id="acc_1" name="Acme Ltd" onClose={vi.fn()} onDeleted={onDeleted} />
        </QueryClientProvider>
      </App>,
    );
    await vi.advanceTimersByTimeAsync(0);
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "Acme Ltd" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /Acme Ltd/ }));
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const jobCalls = () => calls.filter((c) => c.url.includes("/api/v1/jobs/"));
    expect(jobCalls().length).toBe(1);
    act(() => session.set(null));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(jobCalls().length).toBe(1);
    expect(onDeleted).not.toHaveBeenCalled();
    expect(within(dialog).queryByRole("alert")).toBeNull();
    expect(within(dialog).getByRole("status").textContent).toContain("Deleting");
  });
});
