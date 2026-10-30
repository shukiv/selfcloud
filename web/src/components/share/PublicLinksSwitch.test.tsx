// SPDX-License-Identifier: AGPL-3.0-or-later
import { fireEvent, render, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient, queryKey } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import PublicLinksSwitch from "./PublicLinksSwitch";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: `csrf-${id}`,
    publicLinks: true,
  } as unknown as Session;
}

beforeEach(() => session.set(signedAs("olivia")));
afterEach(() => session.set(null));

describe("the account switch answered after the person has changed", () => {
  it("writes no answer into the cache for the person before (guard: the changed check before setQueryData)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((resolve) => {
      release = resolve;
    });
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/accounts/acc_1/sharing-policy" && c.method === "GET") return { status: 200, body: { public_links: true, anyone_links: 0 } };
      if (c.url === "/api/v1/accounts/acc_1/sharing-policy" && c.method === "PATCH") return held;
      return fail(404, "not_found");
    });
    const qc = createQueryClient();
    const oldKey = queryKey("sharing-policy", "acc_1");
    render(
      <QueryClientProvider client={qc}>
        <PublicLinksSwitch account="acc_1" />
      </QueryClientProvider>,
    );
    await waitFor(() => expect((document.getElementById("public-links-switch") as HTMLInputElement).checked).toBe(true));
    fireEvent.click(document.getElementById("public-links-switch") as HTMLElement);
    // The person changes while the change is out: the client is cleared, as the app does at a change of person.
    session.set(signedAs("bob"));
    qc.clear();
    release({ status: 200, body: { public_links: false, anyone_links: 0 } });
    await new Promise((r) => setTimeout(r, 50));
    expect(qc.getQueryData(oldKey)).toBeUndefined();
  });
});
