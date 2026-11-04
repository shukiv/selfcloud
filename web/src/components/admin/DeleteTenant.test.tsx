// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { signedAs, usage } from "../../test/adminFixtures";
import DeleteTenant from "./DeleteTenant";

afterEach(() => session.set(null));

// The deletion request that never answers is cancelled at once: the dialog closes and the request is aborted (fu4, item 2).
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
});
