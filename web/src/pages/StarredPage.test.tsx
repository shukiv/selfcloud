// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { setLang, translate } from "../lib/i18n";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import StarredPage from "./StarredPage";
import { desktopViewport } from "../test/viewport";

// The drive lists draw their row icons inline on a desktop window (tl4): the tests here read the desktop layout.
let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
});
afterEach(() => {
  undoView();
});

function signedAs(id: string, role: "owner" | "member" = "member"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role, locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const starred = (id: string, name: string) => ({
  id,
  folder_id: "fld_a",
  parent_id: null,
  name,
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "viewer",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  starred_at: "2026-10-03T00:00:00Z",
});

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <StarredPage />
    </QueryClientProvider>,
  );
}

const STARRED = "/api/v1/starred";

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
});

describe("Starred icon buttons name their item", () => {
  it("each row's Remove star names the item it removes, so a screen reader can tell the rows apart", async () => {
    stubFetch((c: Call) => {
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf"), starred("nod_s2", "Plan.doc")] } };
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByRole("link", { name: "Budget.pdf" });
    // The row icons draw inline once the breakpoint is known, which is after the table's first draw.
    await screen.findByRole("columnheader", { name: "Starred on" });
    expect(screen.getByRole("button", { name: "Remove star: Budget.pdf" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Remove star: Plan.doc" })).toBeTruthy();
  });
});

describe("Starred", () => {
  it("lists the starred items with their address and a Remove star button, and never a raw ID", async () => {
    stubFetch((c: Call) => {
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")] } };
      return fail(404, "not_found");
    });
    renderPage();
    const link = await screen.findByRole("link", { name: "Budget.pdf" });
    expect(link.getAttribute("href")).toBe("/n/nod_s1");
    // The row's icon comes once the layout has its breakpoint, which can be a tick after the row is drawn: wait for it.
    expect(await screen.findByRole("button", { name: new RegExp(`^${translate("en", "starred.remove")}:`) })).toBeTruthy();
    expect(screen.queryByText("nod_s1")).toBeNull();
  });

  it("a failed first load shows its error and never the empty state (guard: empty only after a successful read)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    await screen.findByRole("alert");
    expect(screen.queryByText(translate("en", "starred.empty"))).toBeNull();
  });

  it("an empty answer shows the empty state", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByText(translate("en", "starred.empty"))).toBeTruthy();
    // The empty state is antd's Empty, in the status region (decision 111).
    expect(document.querySelector('[role="status"] .empty-desc')?.textContent).toBe(translate("en", "starred.empty"));
  });

  it("removing a star sends one request, takes the item off the list, and leaves the item itself", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return { status: 200, body: {} };
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")] } };
      return fail(404, "not_found");
    });
    renderPage();
    const button = await screen.findByRole("button", { name: new RegExp(`^${translate("en", "starred.remove")}:`) });
    fireEvent.click(button);
    fireEvent.click(button);
    await waitFor(() => expect(screen.queryByRole("link", { name: "Budget.pdf" })).toBeNull());
    const deletes = calls.filter((c) => c.method === "DELETE");
    expect(deletes.map((c) => c.url)).toEqual(["/api/v1/nodes/nod_s1/star"]);
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
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_same", `${who}.pdf`)] } };
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: new RegExp(`^${translate("en", "starred.remove")}:`) }));
    who = "bob";
    session.set(signedAs("bob"));
    await screen.findByRole("link", { name: "bob.pdf" });
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByRole("link", { name: "bob.pdf" })).toBeTruthy();
  });

  it("a refused removal keeps the item and shows the error (guard: the list is kept on a failed change)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "DELETE") return fail(500, "internal");
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")] } };
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: new RegExp(`^${translate("en", "starred.remove")}:`) }));
    await screen.findByRole("alert");
    expect(screen.getByRole("link", { name: "Budget.pdf" })).toBeTruthy();
  });

  it("a failed refetch of the next page keeps the list shown, with the error (guard: list stays on a failed load more)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("cursor=")) return fail(500, "internal");
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")], next_cursor: "c2" } };
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByRole("link", { name: "Budget.pdf" });
    fireEvent.click(screen.getByRole("button", { name: translate("en", "files.loadMore") }));
    await screen.findByRole("alert");
    expect(screen.getByRole("link", { name: "Budget.pdf" })).toBeTruthy();
  });

  // An observer whose end of list is always in view fires as soon as it is watched. After a failed next page the page must not
  // make a new one, or it would ask again with no delay; only Load more asks again.
  it("after a failed next page, nothing is asked for until Load more is clicked", async () => {
    class InViewObserver {
      private live = true;
      constructor(private readonly cb: (entries: { isIntersecting: boolean }[]) => void) {}
      observe() {
        queueMicrotask(() => {
          if (this.live) this.cb([{ isIntersecting: true }]);
        });
      }
      disconnect() {
        this.live = false;
      }
      unobserve() {}
      takeRecords() {
        return [];
      }
    }
    vi.stubGlobal("IntersectionObserver", InViewObserver);
    try {
      const { calls } = stubFetch((c: Call) => {
        if (c.url.includes("cursor=")) return fail(500, "internal");
        if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")], next_cursor: "c2" } };
        return fail(404, "not_found");
      });
      const nextPages = () => calls.filter((c) => c.url.includes("cursor=")).length;
      renderPage();
      await screen.findByRole("alert");
      expect(nextPages()).toBe(1);
      await new Promise((r) => setTimeout(r, 100));
      expect(nextPages()).toBe(1);
      fireEvent.click(screen.getByRole("button", { name: translate("en", "files.loadMore") }));
      await waitFor(() => expect(nextPages()).toBe(2));
      await new Promise((r) => setTimeout(r, 100));
      expect(nextPages()).toBe(2);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("a session change clears the last person's list before the next person's read is answered (guard: setItems on a new session)", async () => {
    let who = "ann";
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(async (c: Call) => {
      if (c.url.startsWith(STARRED)) {
        if (who === "bob") await gate;
        return { status: 200, body: { items: [starred(`nod_${who}`, `${who}.pdf`)] } };
      }
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByRole("link", { name: "ann.pdf" });
    who = "bob";
    session.set(signedAs("bob"));
    await waitFor(() => expect(screen.queryByRole("link", { name: "ann.pdf" })).toBeNull());
    release();
    await screen.findByRole("link", { name: "bob.pdf" });
  });

  it("a page opens for an owner and a member alike: no role check here (the menu decides the link)", async () => {
    session.set(signedAs("owen", "owner"));
    stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [] } } : fail(404, "not_found")));
    renderPage();
    const list = await screen.findByText(translate("en", "starred.empty"));
    expect(within(list.parentElement as HTMLElement).getByText(translate("en", "starred.empty"))).toBeTruthy();
  });

  // A change of language keeps the list and does not read it again (the page reads the language through a ref).
  it("a change of language keeps the list and does not read it again (guard: lang read through a ref)", async () => {
    const { calls } = stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [starred("nod_s1", "Plan.txt")] } } : fail(404, "x")));
    renderPage();
    await screen.findByRole("link", { name: "Plan.txt" });
    const before = calls.filter((c) => c.url.startsWith(STARRED)).length;
    await act(async () => {
      await setLang("he");
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(await screen.findByRole("link", { name: "Plan.txt" })).toBeTruthy();
    expect(calls.filter((c) => c.url.startsWith(STARRED)).length).toBe(before);
  });
});
