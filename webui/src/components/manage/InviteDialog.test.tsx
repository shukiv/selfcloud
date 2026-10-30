// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
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
});
