// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import LinksPage from "./LinksPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: true,
  } as unknown as Session;
}

const LINK = {
  id: "shl_1",
  node_id: "nod_1",
  audience: "anyone",
  role: "viewer",
  upload_only: false,
  expires_at: null,
  created_at: "2026-10-01T00:00:00Z",
  paused: false,
  password_set: false,
  created_by: "usr_owen",
  created_by_kind: "user",
  created_by_name: "Owen",
  folder_id: "fld_1",
  folder_kind: "team",
  folder_name: "Plans",
  folder_owner_id: "",
  folder_owner_name: "",
  item_name: "report.pdf",
  item_path: "Plans/report.pdf",
  hidden: false,
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <LinksPage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const isLinkDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/share-links/shl_1";

describe("links", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/share-links") ? fail(403, "forbidden") : { status: 200, body: { anyone_links: 0, public_links: true } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No links to show.")).toBeNull();
  });

  it("a failed read of the count shows an error, not a zero (guard: no silent zero for the delete-all count)", async () => {
    stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return fail(403, "forbidden");
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    expect(await screen.findByText("report.pdf")).toBeTruthy();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByRole("button", { name: "Delete all public links" })).toBeNull();
  });

  it("the audience filter asks the server for that audience (guard: filter in the request)", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    await screen.findByText("report.pdf");
    const selector = document.getElementById("links-audience")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("Anyone with the link");
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => c.url.includes("audience=anyone"))).toBe(true));
  });

  it("deleting a link asks first and sends one request for a double click (guard: confirm and once-at-a-time)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isLinkDelete(c)) return held;
      if (c.url.includes("/sharing-policy")) return { status: 200, body: { public_links: true, anyone_links: 1 } };
      return { status: 200, body: { items: [LINK] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Delete link" }));
    const go = (await screen.findAllByRole("button", { name: /Delete link/ })).at(-1)!;
    act(() => {
      fireEvent.click(go);
      fireEvent.click(go);
    });
    await waitFor(() => expect(calls.filter(isLinkDelete)).toHaveLength(1));
    release({ status: 204 });
    expect(await screen.findByText("Link deleted.")).toBeTruthy();
  });
});
