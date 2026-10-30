// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { setLang, translate } from "../lib/i18n";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import RecentPage from "./RecentPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const recent = (id: string, name: string, viewed_at: string | null = "2026-10-02T00:00:00Z") => ({
  id,
  folder_id: "fld_a",
  parent_id: null,
  name,
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "owner",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  viewed_at,
  edited_at: null,
  last_at: "2026-10-02T00:00:00Z",
});

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <RecentPage />
    </QueryClientProvider>,
  );
}

const RECENT = "/api/v1/recent";
const CLEAR = translate("en", "recent.clear");

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
});

describe("Recent", () => {
  it("lists the recent items, each with what the person did last and its address", async () => {
    stubFetch((c: Call) => (c.url.startsWith(RECENT) ? { status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } } : fail(404, "x")));
    renderPage();
    const link = await screen.findByRole("link", { name: "Plan.txt" });
    expect(link.getAttribute("href")).toBe("/n/nod_r1");
    expect(screen.getByText(/^Opened /)).toBeTruthy();
  });

  it("a failed first load shows its error and never the empty state (guard: empty only after a successful read)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    await screen.findByRole("alert");
    expect(screen.queryByText(translate("en", "recent.empty"))).toBeNull();
  });

  it("an empty answer shows the empty state and no Clear button", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByText(translate("en", "recent.empty"))).toBeTruthy();
    expect(screen.queryByRole("button", { name: CLEAR })).toBeNull();
  });

  it("removing one item sends its removal and takes it off the list only", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return { status: 200, body: {} };
      if (c.url.startsWith(RECENT)) return { status: 200, body: { items: [recent("nod_r1", "Plan.txt"), recent("nod_r2", "Notes.txt")] } };
      return fail(404, "x");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: translate("en", "recent.removeName", { name: "Plan.txt" }) }));
    await waitFor(() => expect(screen.queryByRole("link", { name: "Plan.txt" })).toBeNull());
    expect(screen.getByRole("link", { name: "Notes.txt" })).toBeTruthy();
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.url)).toEqual(["/api/v1/recent/nod_r1"]);
  });

  it("a removal answered after the session changed does not touch the next person's list (guard: sessionChanged before the await)", async () => {
    let who = "ann";
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(async (c: Call) => {
      if (c.method === "DELETE") {
        await gate;
        return { status: 200, body: {} };
      }
      if (c.url.startsWith(RECENT)) return { status: 200, body: { items: [recent("nod_same", `${who}.txt`)] } };
      return fail(404, "x");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: translate("en", "recent.removeName", { name: "ann.txt" }) }));
    who = "bob";
    session.set(signedAs("bob"));
    await screen.findByRole("link", { name: "bob.txt" });
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByRole("link", { name: "bob.txt" })).toBeTruthy();
  });

  it("a refused removal keeps the item and shows the error (guard: the list is kept on a failed change)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "DELETE") return fail(500, "internal");
      return { status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: translate("en", "recent.removeName", { name: "Plan.txt" }) }));
    await screen.findByRole("alert");
    expect(screen.getByRole("link", { name: "Plan.txt" })).toBeTruthy();
  });

  it("Clear all asks first: Cancel sends nothing", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } }));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: CLEAR }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: translate("en", "common.cancel") }));
    await new Promise((r) => setTimeout(r, 20));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    expect(screen.getByRole("link", { name: "Plan.txt" })).toBeTruthy();
  });

  it("a click outside the confirmation does not close it (guard: maskClosable false)", async () => {
    stubFetch(() => ({ status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } }));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: CLEAR }));
    const dialog = await screen.findByRole("dialog");
    const mask = document.querySelector(".ant-modal-wrap");
    expect(mask).not.toBeNull();
    fireEvent.click(mask as Element);
    await new Promise((r) => setTimeout(r, 300));
    // A closing dialog gets the leave motion class; a dialog kept open does not.
    expect(dialog.className).not.toMatch(/leave/);
  });

  it("confirming clears the list with one request, even on a double click", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return { status: 200, body: {} };
      return { status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: CLEAR }));
    const dialog = await screen.findByRole("dialog");
    const ok = within(dialog).getByRole("button", { name: CLEAR });
    fireEvent.click(ok);
    fireEvent.click(ok);
    await waitFor(() => expect(screen.queryByRole("link", { name: "Plan.txt" })).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.url)).toEqual(["/api/v1/recent"]);
  });

  it("a failed clear keeps the confirmation open with the error inside it", async () => {
    stubFetch((c: Call) => {
      if (c.method === "DELETE") return fail(500, "internal");
      return { status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: CLEAR }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: CLEAR }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert")).toBeTruthy());
    expect(screen.getByRole("link", { name: "Plan.txt" })).toBeTruthy();
  });

  it("a session change clears the last person's list (guard: setItems on a new session)", async () => {
    let who = "ann";
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(async (c: Call) => {
      if (c.url.startsWith(RECENT)) {
        if (who === "bob") await gate;
        return { status: 200, body: { items: [recent(`nod_${who}`, `${who}.txt`)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "ann.txt" });
    who = "bob";
    session.set(signedAs("bob"));
    await waitFor(() => expect(screen.queryByRole("link", { name: "ann.txt" })).toBeNull());
    release();
    await screen.findByRole("link", { name: "bob.txt" });
  });

  // A change of language keeps the list and does not read it again (the page reads the language through a ref).
  it("a change of language keeps the list and does not read it again (guard: lang read through a ref)", async () => {
    const { calls } = stubFetch((c: Call) => (c.url.startsWith(RECENT) ? { status: 200, body: { items: [recent("nod_r1", "Plan.txt")] } } : fail(404, "x")));
    renderPage();
    await screen.findByRole("link", { name: "Plan.txt" });
    const before = calls.filter((c) => c.url.startsWith(RECENT)).length;
    await act(async () => {
      await setLang("he");
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByRole("link", { name: "Plan.txt" })).toBeTruthy();
    expect(calls.filter((c) => c.url.startsWith(RECENT)).length).toBe(before);
  });
});
