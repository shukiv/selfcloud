// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import DevicesPage from "./DevicesPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "owner", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const device = (id: string, client_name: string) => ({
  id,
  client_name,
  client_ip: "",
  user_agent: "",
  created_at: "2026-11-10T08:00:00Z",
  expires_at: "2026-12-10T08:00:00Z",
});

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <DevicesPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs("olivia")));
afterEach(() => session.set(null));

describe("signed-in devices", () => {
  it("lists the person's devices with the name, the sign-in time and the expiry", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/devices") return { status: 200, body: { devices: [device("dev_1", "laptop")] } };
      return fail(404, "not_found");
    });
    renderPage();
    const row = (await screen.findByText("laptop")).closest("tr");
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getAllByText(/2026/).length).toBe(2);
  });

  it("shows the terminal's address in its own column, as the approval page showed it", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/devices") {
        return { status: 200, body: { devices: [{ ...device("dev_1", "laptop"), client_ip: "198.51.100.23", user_agent: "selfcloud/1" }] } };
      }
      return fail(404, "not_found");
    });
    renderPage();
    const row = (await screen.findByText("laptop")).closest("tr") as HTMLElement;
    expect(within(row).getByText("198.51.100.23")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Address" })).toBeTruthy();
  });

  it("with no device, says so as the title, and offers no add action", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/devices") return { status: 200, body: { devices: [] } };
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByText("No devices are signed in");
    expect(screen.getByText(/Sign in from a terminal/)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("the revoke icon names the device, and asks before it ends the sign-in", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/devices" && c.method === "GET") return { status: 200, body: { devices: [device("dev_1", "laptop")] } };
      if (c.url === "/api/v1/auth/cli/devices/dev_1") return { status: 204 };
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByText("laptop");
    fireEvent.click(screen.getByRole("button", { name: "Sign out this device: laptop" }));
    // Nothing is sent until the person confirms.
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(await screen.findByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/v1/auth/cli/devices/dev_1")).toBe(true));
  });
});
