// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { expectEmptyTable } from "../../test/emptyTable";
import { DESKTOP, PHONE, setViewport } from "../../test/viewport";
import PeoplePage from "./PeoplePage";

const ME = { id: "usr_owen", email: "owen@example.com", display_name: "Owen Owner", role: "owner", status: "active" };

function person(id: string, status: string, role = "member") {
  return {
    id,
    external_id: null,
    email: `${id}@example.com`,
    display_name: `Name ${id}`,
    locale: "en",
    role,
    status,
    verified_at: null,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  };
}

function signedAs(): Session {
  return {
    user: { ...ME, locale: "en" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

function renderPage(qc: QueryClient = createQueryClient()) {
  render(
    <QueryClientProvider client={qc}>
      <PeoplePage />
    </QueryClientProvider>,
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

describe("the people list", () => {
  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers, with the title, the description and Invite person.
  it("an empty list keeps its table and headers, with the Empty in the body and Invite person, which opens the form", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const invite = await expectEmptyTable("People", {
      headers: ["Name", "Role", "Status"],
      title: "No people yet",
      description: "Invite the first person to start.",
      action: "Invite person",
    });
    fireEvent.click(invite as HTMLElement);
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No people yet")).toBeNull();
  });

  it("a failed refetch keeps the rows already shown, with a warning (guard: list stays on a failed refetch)", async () => {
    let broken = false;
    stubFetch((c: Call) => {
      if (c.url.includes("/users")) return broken ? fail(403, "forbidden") : { status: 200, body: { items: [person("usr_a", "active")] } };
      return { status: 200, body: { items: [] } };
    });
    const qc = renderPage();
    expect(await screen.findByText("Name usr_a")).toBeTruthy();
    broken = true;
    await act(async () => {
      await qc.refetchQueries({ type: "all" });
    });
    await waitFor(() => expect(screen.getByRole("alert").className).toContain("ant-alert-warning"));
    expect(screen.getByText("Name usr_a")).toBeTruthy();
  });

  it("the menu offers only what the person's state allows: a pending person is resent, not disabled (guard: the status rules in the menu)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_p", "pending")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await screen.findByText("Name usr_p");
    fireEvent.click(screen.getByRole("button", { name: /More actions for Name usr_p/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("Resend invitation")).toBeTruthy();
    expect(within(menu).queryByText("Disable")).toBeNull();
    expect(within(menu).getByText("Remove…")).toBeTruthy();
  });

  // design-tl 3.2 (M1): the "Open" text button goes; the row opens the person, by a click or by Enter on the focused row.
  it("a row opens the person's details on Enter, and no row carries an Open button", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_a", "active")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const row = await screen.findByRole("row", { name: "Name usr_a" });
    expect(screen.queryByRole("button", { name: /^Details/ })).toBeNull();
    fireEvent.keyDown(row, { key: "Enter" });
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  // review L1: the name is a link-like button, so a keyboard user can open the person from the name itself.
  it("the name is a button that opens the person's details (review L1)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_a", "active")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Name usr_a" }));
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  // review L5: the email line takes its colour from the theme token, not from a CSS variable.
  it("the email line's colour is a theme token, not a CSS variable (review L5)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_a", "active")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const line = (await screen.findByText("usr_a@example.com")).parentElement as HTMLElement;
    expect(line.style.color).toMatch(/^rgba?\(/);
  });

  // design-tl 2.2 and the owner's rule: the signed-in person's own row reads "You".
  it("the signed-in person's own row reads You", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_owen", "active", "owner"), person("usr_a", "active")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const mine = await screen.findByRole("row", { name: "Name usr_owen" });
    expect(within(mine).getByText("You")).toBeTruthy();
    expect(within(screen.getByRole("row", { name: "Name usr_a" })).queryByText("You")).toBeNull();
  });

  // design-tl 4: below md the role and the status are the name's second line, and the actions are the ⋮ menu alone.
  it("on a phone, the role and the status are the name's second line, and the row has its ⋮ button alone", async () => {
    restoreViewport();
    restoreViewport = setViewport(PHONE);
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_a", "active")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const row = await screen.findByRole("row", { name: "Name usr_a" });
    expect(row.querySelector(".row-second")?.textContent).toBe("Member · Active");
    const headers = within(screen.getByRole("region", { name: "People" })).getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).not.toContain("Role");
    expect(headers).not.toContain("Status");
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["More actions for Name usr_a"]);
  });
});

describe("inviting a person", () => {
  it("the verification link in the answer is never shown or kept in the cache (guard: the reply is not stored)", async () => {
    const SECRET = "tok-SECRET-LINK-0123456789";
    stubFetch((c: Call) => {
      if (c.method === "POST" && c.url.endsWith("/users")) {
        return {
          status: 201,
          body: { ...person("usr_new", "pending"), verification: { method: "link", url: `https://x.example/verify/${SECRET}` } },
        };
      }
      if (c.url.includes("/users")) return { status: 200, body: { items: [] } };
      return { status: 200, body: { items: [] } };
    });
    const qc = renderPage();
    // The header's button and the empty table's action are both Invite person; the header's comes first.
    fireEvent.click((await screen.findAllByRole("button", { name: /Invite person/ }))[0]);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "new@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /Send invitation/ }));
    expect(await screen.findByText("The email was sent.")).toBeTruthy();
    expect(new XMLSerializer().serializeToString(document)).not.toContain(SECRET);
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data ?? null))).not.toContain(SECRET);
  });
});
