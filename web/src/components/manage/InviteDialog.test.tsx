// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { stubFetch } from "../../test/fetchStub";
import InviteDialog from "./InviteDialog";

function signedAs(): Session {
  return {
    user: { id: "usr_owner", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

describe("the invite dialog", () => {
  // The first field the person needs is the email, so it has the focus when the dialog opens (guard: autoFocus on the email).
  it("opens with the email field focused", async () => {
    stubFetch(() => ({ status: 200, body: {} }));
    render(
      <QueryClientProvider client={createQueryClient()}>
        <InviteDialog account="acc_1" onclose={vi.fn()} oncreated={vi.fn()} />
      </QueryClientProvider>,
    );
    const email = await screen.findByLabelText("Email");
    await waitFor(() => expect(document.activeElement).toBe(email));
  });

  // A busy dialog can be cancelled: the request is aborted and the dialog closes, without waiting for an answer that may
  // never come. The page says the invitation may or may not have gone out (guard: Cancel on a busy dialog).
  it("Cancel on a busy dialog aborts the invitation request and closes the dialog", async () => {
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
    const onclose = vi.fn();
    const qc = createQueryClient();
    // A cancelled write may have gone through, so the lists are read again (guard: a cancelled write refreshes the lists).
    const refresh = vi.spyOn(qc, "invalidateQueries");
    render(
      <App>
        <QueryClientProvider client={qc}>
          <InviteDialog account="acc_1" onclose={onclose} oncreated={vi.fn()} />
        </QueryClientProvider>
      </App>,
    );
    fireEvent.change(await screen.findByLabelText("Email"), { target: { value: "ana@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    expect(signals[0].aborted).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalled();
    expect(await screen.findByText("Cancelled. The change may or may not have been saved; check the list before you try again.")).toBeTruthy();
  });
});
