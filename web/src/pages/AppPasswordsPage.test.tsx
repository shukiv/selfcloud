// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import AppPasswordsPage from "./AppPasswordsPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

interface Item {
  id: string;
  name: string;
  access: "read" | "write";
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

const apw = (id: string, name: string, extra: Partial<Item> = {}): Item => ({
  id,
  name,
  access: "read",
  prefix: `scp_${id}_Qx7vT2`,
  created_at: "2026-11-10T08:00:00Z",
  last_used_at: null,
  ...extra,
});

const path = "/api/v1/me/app-passwords";
const mountOff = "The administrator has not turned on the drive mount.";

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <AppPasswordsPage />
    </QueryClientProvider>,
  );
}

// list answers the list route; made answers the create route when one is given.
function list(items: Item[], mount_enabled: boolean, made?: { status: number; body?: unknown }) {
  return stubFetch((c: Call) => {
    if (c.url === path && c.method === "GET") return { status: 200, body: { items, mount_enabled } };
    if (c.url === path && c.method === "POST" && made) return made;
    if (c.url.startsWith(`${path}/`) && c.method === "DELETE") return { status: 204 };
    return fail(404, "not_found");
  });
}

beforeEach(() => session.set(signedAs("olivia")));
afterEach(() => session.set(null));

describe("the App passwords page", () => {
  it("lists the rows with name, access, created and last used, and Never for no last use", async () => {
    list([apw("apw_1", "laptop"), apw("apw_2", "desktop", { access: "write", last_used_at: "2026-11-12T09:00:00Z" })], true);
    renderPage();
    const laptop = (await screen.findByText("laptop")).closest("tr") as HTMLElement;
    expect(within(laptop).getByText("Read only")).toBeTruthy();
    expect(within(laptop).getByText("Never")).toBeTruthy();
    expect(within(laptop).getAllByText(/2026/).length).toBe(1);
    const desktop = screen.getByText("desktop").closest("tr") as HTMLElement;
    expect(within(desktop).getByText("Read and write")).toBeTruthy();
    expect(within(desktop).getAllByText(/2026/).length).toBe(2);
    // The column headers name the four columns.
    for (const title of ["Name", "Access", "Created", "Last used"]) {
      expect(screen.getByRole("columnheader", { name: title })).toBeTruthy();
    }
  });

  it("is the unified list preset: a region named for the page", async () => {
    list([apw("apw_1", "laptop")], true);
    renderPage();
    await screen.findByText("laptop");
    const region = screen.getByRole("region", { name: "App passwords" });
    expect(region.classList.contains("item-list")).toBe(true);
  });

  it("with the mount off, says so and offers no New, while the table and Revoke still work", async () => {
    const { calls } = list([apw("apw_1", "laptop")], false);
    renderPage();
    await screen.findByText("laptop");
    expect(screen.getByText(mountOff)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "New app password" })).toBeNull();
    // Revoke still works while the mount is off.
    fireEvent.click(screen.getByRole("button", { name: "Revoke laptop" }));
    const pop = (await screen.findByText(/Revoke laptop\?/)).closest(".ant-popconfirm") as HTMLElement;
    fireEvent.click(within(pop).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === `${path}/apw_1`)).toBe(true));
  });

  it("New validates the name before sending: empty and over 64 characters send nothing", async () => {
    const { calls } = list([], true);
    renderPage();
    fireEvent.click((await screen.findAllByRole("button", { name: "New app password" }))[0]);
    const name = await screen.findByLabelText("Name");
    // Empty: the create is refused in the form, with no request.
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("Enter a name.")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    // Whitespace only is empty too.
    fireEvent.change(name, { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    // Over 64 characters is refused in the form as well.
    fireEvent.change(name, { target: { value: "a".repeat(65) } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("The name is at most 64 characters.")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    // A 64-character name is allowed.
    fireEvent.change(name, { target: { value: "a".repeat(64) } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  });

  it("a create shows the secret once in ShowOnce and reads the list again", async () => {
    const secret = "scp_apw_1_randomtail";
    let reads = 0;
    const { calls } = stubFetch((c: Call) => {
      if (c.url === path && c.method === "GET") {
        reads++;
        return { status: 200, body: { items: reads > 1 ? [apw("apw_1", "laptop")] : [], mount_enabled: true } };
      }
      if (c.url === path && c.method === "POST") return { status: 201, body: { app_password: apw("apw_1", "laptop"), secret } };
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click((await screen.findAllByRole("button", { name: "New app password" }))[0]);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "  laptop  " } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    // The secret shows once, in the ShowOnce dialog, with its copy button and its warning.
    const field = (await screen.findByDisplayValue(secret)) as HTMLInputElement;
    expect(field.readOnly).toBe(true);
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(screen.getByText(/not shown again/)).toBeTruthy();
    // The name was trimmed, and no access field was sent.
    const post = calls.find((c) => c.method === "POST");
    expect(post?.body).toEqual({ name: "laptop" });
    // The list is read again after the create.
    await waitFor(() => expect(reads).toBeGreaterThanOrEqual(2));
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    await waitFor(() => expect(screen.queryByDisplayValue(secret)).toBeNull());
    expect(await screen.findByText("laptop")).toBeTruthy();
  });

  it("Revoke asks first and then sends DELETE", async () => {
    const { calls } = list([apw("apw_1", "laptop")], true);
    renderPage();
    await screen.findByText("laptop");
    fireEvent.click(screen.getByRole("button", { name: "Revoke laptop" }));
    const pop = (await screen.findByText(/Revoke laptop\?/)).closest(".ant-popconfirm") as HTMLElement;
    // Nothing is sent until the person confirms.
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(within(pop).getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === `${path}/apw_1`)).toBe(true));
  });

  it("the cap's answer says to revoke one before making another", async () => {
    list([], true, { status: 409, body: { error: { code: "conflict", details: { reason: "limit", limit: 20 } } } });
    renderPage();
    fireEvent.click((await screen.findAllByRole("button", { name: "New app password" }))[0]);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "one too many" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("You have 20 app passwords, the most allowed. Revoke one before making another.")).toBeTruthy();
  });

  it("a create turned off meanwhile shows the alert and reads the list again", async () => {
    let reads = 0;
    stubFetch((c: Call) => {
      if (c.url === path && c.method === "GET") {
        reads++;
        return { status: 200, body: { items: [], mount_enabled: reads === 1 } };
      }
      if (c.url === path && c.method === "POST") return fail(404, "not_found");
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click((await screen.findAllByRole("button", { name: "New app password" }))[0]);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "laptop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText(mountOff)).toBeTruthy();
    await waitFor(() => expect(reads).toBeGreaterThanOrEqual(2));
    expect(screen.queryByRole("button", { name: "New app password" })).toBeNull();
  });

  it("a rate-limited create shows the app's usual rate-limit text", async () => {
    list([], true, { status: 429, body: { error: { code: "rate_limited", details: {} } } });
    renderPage();
    fireEvent.click((await screen.findAllByRole("button", { name: "New app password" }))[0]);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "laptop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText(/Too many attempts\. Try again in 1 minute\./)).toBeTruthy();
  });

  it("the empty state keeps the table frame, and offers New only when the mount is on", async () => {
    list([], true);
    renderPage();
    expect(await screen.findByText("No app passwords")).toBeTruthy();
    // The title's New and the empty state's own action.
    expect(screen.getAllByRole("button", { name: "New app password" }).length).toBe(2);
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeTruthy();
  });

  it("the empty state offers no New when the mount is off", async () => {
    list([], false);
    renderPage();
    expect(await screen.findByText("No app passwords")).toBeTruthy();
    expect(screen.getByText(mountOff)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "New app password" })).toBeNull();
  });
});
