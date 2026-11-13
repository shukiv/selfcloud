// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { expectEmptyTable } from "../../test/emptyTable";
import { DESKTOP, PHONE, setViewport } from "../../test/viewport";
import type { Names } from "../../lib/audit/sentence";
import GroupsPage, { useMemberRows } from "./GroupsPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const GROUP = { id: "grp_1", external_id: null, name: "Finance", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" };

function renderPage() {
  const qc = createQueryClient();
  render(
    <App>
      <QueryClientProvider client={qc}>
        <GroupsPage />
      </QueryClientProvider>
    </App>,
  );
  return qc;
}

let restoreViewport: () => void = () => undefined;
beforeEach(() => {
  session.set(signedAs());
  restoreViewport = setViewport(DESKTOP);
});
afterEach(() => {
  session.set(null);
  restoreViewport();
});

const isGroupDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/accounts/acc_1/groups/grp_1";

describe("groups", () => {
  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its header, with the title, the description and New group.
  it("an empty list keeps its table and its Name header, with the Empty and New group in the body", async () => {
    stubFetch((c) => (c.url.includes("/groups") ? { status: 200, body: { items: [] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await expectEmptyTable("Groups", {
      headers: ["Name"],
      title: "No groups yet",
      description: "A group lets you share with several people at once.",
      action: "New group",
    });
  });

  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/groups") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText(/No groups yet/)).toBeNull();
  });

  // Members, Rename and Delete are the row's three icons, each with its tooltip and a name for a screen reader.
  it("each group has its three icons inline on a desktop, named for the group", async () => {
    stubFetch((c) => (c.url.includes("/groups") && c.method === "GET" ? { status: 200, body: { items: [GROUP] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const row = await screen.findByRole("row", { name: "Finance" });
    // A group has no menu entries of its own, so the row has no ⋮ button on a desktop.
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["Members: Finance", "Rename: Finance", "Delete: Finance"]);
  });

  // on a phone the three icons are the ⋮ menu, and the list keeps its one Name column.
  it("on a phone the group's actions are one ⋮ menu, with Members, Rename and Delete in it", async () => {
    restoreViewport();
    restoreViewport = setViewport(PHONE);
    stubFetch((c) => (c.url.includes("/groups") && c.method === "GET" ? { status: 200, body: { items: [GROUP] } } : { status: 200, body: { items: [] } }));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "More actions for Finance" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("Members: Finance")).toBeTruthy();
    expect(within(menu).getByText("Rename: Finance")).toBeTruthy();
    expect(within(menu).getByText("Delete: Finance")).toBeTruthy();
  });

  it("a double click on delete sends one request (guard: the once-at-a-time check)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isGroupDelete(c)) return held;
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    const go = await screen.findByRole("button", { name: /^Delete$/ });
    act(() => {
      fireEvent.click(go);
      fireEvent.click(go);
    });
    await waitFor(() => expect(calls.filter(isGroupDelete)).toHaveLength(1));
    release({ status: 204 });
  });

  it("a delete that never answers: Cancel closes the dialog and aborts the request (fu4, item 2)", async () => {
    const { calls } = stubFetch((c) => {
      if (isGroupDelete(c)) return new Promise<Answer>(() => undefined);
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Delete$/ }));
    await waitFor(() => expect(calls.filter(isGroupDelete)).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find(isGroupDelete)?.signal?.aborted).toBe(true);
  });

  it("a deletion answered after the person has changed shows nothing (guard: sessionChanged before the await)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isGroupDelete(c)) return held;
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Delete$/ }));
    await waitFor(() => expect(calls.filter(isGroupDelete)).toHaveLength(1));
    // Another owner signs in: the page stays mounted, so a late write would be visible to the next person.
    session.set({ ...signedAs(), user: { ...signedAs().user, id: "usr_bob" } } as Session);
    release({ status: 204 });
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByText("Finance was deleted.")).toBeNull();
    expect(calls.filter(isGroupDelete)).toHaveLength(1);
  });

  it("a refused deletion keeps the dialog open with the error by code, and not the server text (guard: the error stays in the confirm)", async () => {
    stubFetch((c) => {
      if (isGroupDelete(c)) return { status: 403, body: { error: { code: "forbidden", message: "SERVER-TEXT-XYZ" } } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Delete$/ }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("SERVER-TEXT-XYZ");
    expect(screen.getByRole("button", { name: /Delete$/ })).toBeTruthy();
  });
});

describe("the open group after a rename", () => {
  // The heading shows the name the list has now, not the name the group had when it was opened (guard: the header reads the list).
  it("the members heading takes the new name once the rename is saved", async () => {
    let renamed = false;
    stubFetch((c) => {
      if (c.method !== "GET" && c.url === "/api/v1/accounts/acc_1/groups/grp_1") {
        renamed = true;
        return { status: 200, body: { ...GROUP, name: "Accounts" } };
      }
      if (c.url.includes("/groups/grp_1/members")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [renamed ? { ...GROUP, name: "Accounts" } : GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Members.*Finance/ }));
    await waitFor(() => expect(document.getElementById("members-title")?.textContent).toBe("Finance"));
    fireEvent.click(screen.getByRole("button", { name: /Rename.*Finance/ }));
    fireEvent.change(await screen.findByLabelText("Group name"), { target: { value: "Accounts" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(document.getElementById("members-title")?.textContent).toBe("Accounts"));
  });

  // a group's members are a table too; with no member the table keeps its Name header, and the body says so.
  it("a group with no members keeps its table and header, and the body's title says there are none", async () => {
    stubFetch((c) => {
      if (c.url.includes("/groups/grp_1/members")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Members.*Finance/ }));
    const region = await screen.findByRole("region", { name: "Members" });
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name"]);
    await waitFor(() => expect(region.querySelector("tbody .empty-title")?.textContent).toBe("No members yet"));
    expect(region.querySelector("tbody .empty-desc")).toBeNull();
    expect(region.querySelector("tbody button")).toBeNull();
  });

  // The members rows: each member is a row with its name and a Remove icon with the member's name in its label.
  it("each member has a Remove icon named for the member", async () => {
    stubFetch((c) => {
      if (c.url.includes("/groups/grp_1/members")) return { status: 200, body: { items: [{ user_id: "usr_dana", created_at: "2026-10-01T00:00:00Z" }] } };
      if (c.url.includes("/people") || c.url.includes("/names")) return { status: 200, body: { items: [{ id: "usr_dana", display_name: "Dana", email: "dana@example.com", status: "active", role: "member" }] } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Members.*Finance/ }));
    const region = await screen.findByRole("region", { name: "Members" });
    expect(await within(region).findByRole("button", { name: /^Remove: / })).toBeTruthy();
  });

  // decision 114: the open group's Members icon is pressed, so a screen reader says which group's members show.
  // Only the open group is pressed; the others have no pressed state at all (review M1).
  const OTHER = { ...GROUP, id: "grp_2", name: "Sales" };
  const twoGroups = () =>
    stubFetch((c) => {
      if (c.url.includes("/groups/grp_1/members")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/groups/grp_2/members")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP, OTHER] } };
      return { status: 200, body: { items: [] } };
    });

  it("the open group's Members icon is aria-pressed=true, and no other group's icon has a pressed state (review M1)", async () => {
    twoGroups();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Members: Finance" }));
    await screen.findByRole("region", { name: "Members" });
    expect(screen.getByRole("button", { name: "Members: Finance" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Members: Sales" }).hasAttribute("aria-pressed")).toBe(false);
  });

  it("the pressed Members icon has the pressed look, the class IconButton draws for it (review M1, visual cue)", async () => {
    twoGroups();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Members: Finance" }));
    await screen.findByRole("region", { name: "Members" });
    expect(screen.getByRole("button", { name: "Members: Finance" }).classList.contains("icon-btn--pressed")).toBe(true);
    expect(screen.getByRole("button", { name: "Members: Sales" }).classList.contains("icon-btn--pressed")).toBe(false);
  });

  it("opening another group moves the pressed state to it (review M1)", async () => {
    twoGroups();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Members: Finance" }));
    await screen.findByRole("region", { name: "Members" });
    fireEvent.click(screen.getByRole("button", { name: "Members: Sales" }));
    await waitFor(() => expect(document.getElementById("members-title")?.textContent).toBe("Sales"));
    expect(screen.getByRole("button", { name: "Members: Sales" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Members: Finance" }).hasAttribute("aria-pressed")).toBe(false);
  });

  it("a second click on the open group's Members icon closes its members, and nothing is pressed (review M1)", async () => {
    twoGroups();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Members: Finance" }));
    await screen.findByRole("region", { name: "Members" });
    fireEvent.click(screen.getByRole("button", { name: "Members: Finance" }));
    await waitFor(() => expect(screen.queryByRole("region", { name: "Members" })).toBeNull());
    expect(document.getElementById("members-title")).toBeNull();
    expect(screen.getByRole("button", { name: "Members: Finance" }).hasAttribute("aria-pressed")).toBe(false);
  });
});

describe("the member rows", () => {
  const names = (people: Record<string, string>): Names => ({ person: (id) => people[id], space: () => undefined });

  // Listing's row memo compares the row object: the same inputs must give the same rows, or every row's end redraws.
  it("rendered again with the same ids, lookup and unknown, it returns the same array and the same rows", () => {
    const ids = ["usr_dana", "usr_eli"];
    const lookup = names({ usr_dana: "Dana", usr_eli: "Eli" });
    const { result, rerender } = renderHook(({ ids, lookup, unknown }) => useMemberRows(ids, lookup, unknown), {
      initialProps: { ids, lookup, unknown: "Unknown person" },
    });
    const first = result.current;
    expect(first.map((r) => r.name)).toEqual(["Dana", "Eli"]);
    rerender({ ids, lookup, unknown: "Unknown person" });
    expect(result.current).toBe(first);
    result.current.forEach((row, i) => expect(row).toBe(first[i]));
  });

  it("a new ids array from new query data returns a new array", () => {
    const lookup = names({ usr_dana: "Dana" });
    const { result, rerender } = renderHook(({ ids }) => useMemberRows(ids, lookup, "Unknown person"), {
      initialProps: { ids: ["usr_dana"] },
    });
    const first = result.current;
    rerender({ ids: ["usr_dana"] });
    expect(result.current).not.toBe(first);
    expect(result.current.map((r) => r.name)).toEqual(["Dana"]);
  });

  it("a new lookup once the names load returns new rows with the new names", () => {
    const { result, rerender } = renderHook(({ lookup }) => useMemberRows(["usr_dana"], lookup, "Unknown person"), {
      initialProps: { lookup: names({}) },
    });
    const first = result.current;
    expect(first.map((r) => r.name)).toEqual(["Unknown person"]);
    const loaded = names({ usr_dana: "Dana" });
    rerender({ lookup: loaded });
    expect(result.current).not.toBe(first);
    expect(result.current.map((r) => r.name)).toEqual(["Dana"]);
  });

  it("an id the lookup does not know gets the unknown text", () => {
    const { result } = renderHook(() => useMemberRows(["usr_gone"], names({ usr_dana: "Dana" }), "Unknown person"));
    expect(result.current).toEqual([{ id: "usr_gone", name: "Unknown person" }]);
  });

  it("with ids undefined it returns [], and the same [] when rendered again", () => {
    const lookup = names({});
    const { result, rerender } = renderHook(({ ids }) => useMemberRows(ids, lookup, "Unknown person"), {
      initialProps: { ids: undefined as string[] | undefined },
    });
    const first = result.current;
    expect(first).toEqual([]);
    rerender({ ids: undefined });
    expect(result.current).toBe(first);
  });
});
