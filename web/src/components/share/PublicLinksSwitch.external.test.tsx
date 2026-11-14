// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
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

function renderSwitch(policy: { public_links: boolean; anyone_links: number; external_sharing?: boolean }) {
  const { calls } = stubFetch((c: Call) => {
    if (c.url === "/api/v1/accounts/acc_1/sharing-policy" && c.method === "GET") return { status: 200, body: policy };
    if (c.url === "/api/v1/accounts/acc_1/sharing-policy" && c.method === "PATCH") {
      const body = c.body as { public_links?: boolean; external_sharing?: boolean };
      return { status: 200, body: { ...policy, ...body } };
    }
    return fail(404, "not_found");
  });
  const qc = createQueryClient();
  const view = render(
    <QueryClientProvider client={qc}>
      <PublicLinksSwitch account="acc_1" />
    </QueryClientProvider>,
  );
  return { calls, qc, ...view };
}

describe("the account's external sharing switch", () => {
  it("shows the second box with its own words, bound to external_sharing", async () => {
    renderSwitch({ public_links: true, anyone_links: 0, external_sharing: true });
    const box = (await waitFor(() => {
      const el = document.getElementById("external-sharing-switch") as HTMLInputElement | null;
      if (!el) throw new Error("no external sharing switch yet");
      return el;
    })) as HTMLInputElement;
    expect(box.checked).toBe(true);
    expect(screenText()).toContain("Share with people in other accounts on this server");
    expect(screenText()).toContain("Both accounts must allow it.");
  });

  it("turning it on patches only external_sharing", async () => {
    const { calls } = renderSwitch({ public_links: true, anyone_links: 0, external_sharing: false });
    const box = await waitFor(() => {
      const el = document.getElementById("external-sharing-switch") as HTMLInputElement | null;
      if (!el) throw new Error("no external sharing switch yet");
      return el;
    });
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.body).toEqual({ external_sharing: true });
    await waitFor(() => expect((document.getElementById("external-sharing-switch") as HTMLInputElement).checked).toBe(true));
  });

  it("the first box still patches only public_links", async () => {
    const { calls } = renderSwitch({ public_links: true, anyone_links: 0, external_sharing: true });
    await waitFor(() => expect((document.getElementById("public-links-switch") as HTMLInputElement).checked).toBe(true));
    fireEvent.click(document.getElementById("public-links-switch") as HTMLElement);
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.body).toEqual({ public_links: false });
  });

  it("an older answer without the flag shows the box off", async () => {
    renderSwitch({ public_links: false, anyone_links: 0 });
    const box = await waitFor(() => {
      const el = document.getElementById("external-sharing-switch") as HTMLInputElement | null;
      if (!el) throw new Error("no external sharing switch yet");
      return el;
    });
    expect(box.checked).toBe(false);
  });
});

function screenText(): string {
  return document.body.textContent ?? "";
}