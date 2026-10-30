// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { myStorage } from "../lib/storage/api";
import { setLang, translate } from "../lib/i18n";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import StoragePage from "./StoragePage";

function signedAs(id: string, role: "owner" | "member" | "admin" = "member"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role, locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const largest = (id: string, name: string, size = 500, in_trash = false) => ({
  id,
  name,
  size,
  versions_bytes: 0,
  modified_at: "2026-10-01T00:00:00Z",
  in_trash,
});

const ME = "/api/v1/me";
const LARGEST = "/api/v1/me/storage/largest";
const STORAGE = { used_bytes: 4_000_000_000, reserved_bytes: 0, quota_bytes: 10_000_000_000 };

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <StoragePage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
  myStorage.set(null);
});

describe("Storage", () => {
  it("shows the figures against the limit, with the meter, and the largest files with their address", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_big", "Movie.mkv", 3_000_000_000, true)] } };
      return fail(404, "x");
    });
    renderPage();
    const meter = await screen.findByRole("meter");
    expect(meter.getAttribute("aria-valuemax")).toBe("10000000000");
    expect(meter.getAttribute("aria-valuenow")).toBe("4000000000");
    const link = await screen.findByRole("link", { name: "Movie.mkv" });
    expect(link.getAttribute("href")).toBe("/n/nod_big");
    expect(screen.getByText(translate("en", "storage.inTrash"))).toBeTruthy();
    expect(screen.queryByText("nod_big")).toBeNull();
  });

  it("the limit row shows No limit when the account has none", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: { ...STORAGE, quota_bytes: null } } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.noLimit"))).toBeTruthy();
    expect(screen.queryByRole("meter")).toBeNull();
  });

  it("a failed first load shows its error and never the empty state (guard: empty only after a successful read)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      return fail(500, "internal");
    });
    renderPage();
    await screen.findByRole("alert");
    expect(screen.queryByText(translate("en", "storage.largestEmpty"))).toBeNull();
  });

  it("an empty answer shows the empty state", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    expect(await screen.findByText(translate("en", "storage.largestEmpty"))).toBeTruthy();
  });

  it("the next page is added to the list; a failed next page keeps the list and shows the error (guard: list kept on a failed load more)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.includes("cursor=")) return fail(500, "internal");
      return { status: 200, body: { items: [largest("nod_1", "One.bin")], next_cursor: "c2" } };
    });
    renderPage();
    await screen.findByRole("link", { name: "One.bin" });
    fireEvent.click(screen.getByRole("button", { name: translate("en", "common.loadMore") }));
    await screen.findByRole("alert");
    expect(screen.getByRole("link", { name: "One.bin" })).toBeTruthy();
  });

  it("an installation admin reads nothing and sees no page (role refusal, guard: admin check before the reads)", async () => {
    session.set(signedAs("ada", "admin"));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [] } }));
    const { container } = renderPage();
    await new Promise((r) => setTimeout(r, 20));
    expect(calls.length).toBe(0);
    expect(container.textContent).toBe("");
  });

  it("a session change clears the last person's files before the next person's read is answered (guard: the list is reset on a new session)", async () => {
    let who = "ann";
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(async (c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) {
        if (who === "bob") await gate;
        return { status: 200, body: { items: [largest(`nod_${who}`, `${who}.bin`)] } };
      }
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "ann.bin" });
    who = "bob";
    session.set(signedAs("bob"));
    await waitFor(() => expect(screen.queryByRole("link", { name: "ann.bin" })).toBeNull());
    release();
    await screen.findByRole("link", { name: "bob.bin" });
    expect(within(document.body).queryByRole("link", { name: "ann.bin" })).toBeNull();
  });

  // A failed read of the figures says so, in place of the meter: a silent gap would look like "no limit".
  it("a failed read of the figures shows the error in place of the meter (guard: figures error shown)", async () => {
    stubFetch((c: Call) => {
      if (c.url === ME) return fail(500, "internal");
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [] } };
      return fail(404, "x");
    });
    renderPage();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("meter")).toBeNull();
  });

  // A change of language keeps the list on screen and does not read it again: the page reads the language when it needs it.
  it("a change of language keeps the list and does not read it again (guard: lang read through a ref)", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === ME) return { status: 200, body: { user: {}, storage: STORAGE } };
      if (c.url.startsWith(LARGEST)) return { status: 200, body: { items: [largest("nod_big", "Movie.mkv", 3_000_000_000, true)] } };
      return fail(404, "x");
    });
    renderPage();
    await screen.findByRole("link", { name: "Movie.mkv" });
    const before = calls.filter((c) => c.url.startsWith(LARGEST)).length;
    await act(async () => {
      await setLang("he");
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByRole("link", { name: "Movie.mkv" })).toBeTruthy();
    expect(calls.filter((c) => c.url.startsWith(LARGEST)).length).toBe(before);
  });
});
