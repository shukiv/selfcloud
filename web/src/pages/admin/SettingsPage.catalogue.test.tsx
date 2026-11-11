// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { stubFetch } from "../../test/fetchStub";
import { info, signedAs } from "../../test/adminFixtures";
import { settingsAnswer } from "../../test/settingsFixtures";
import SettingsPage from "./SettingsPage";

// The admin catalogue cannot be loaded: its chunk is missing.
vi.mock("../../i18n/admin/en.json", () => {
  throw new Error("chunk missing");
});

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("the page when its own strings do not load", () => {
  it("keeps the heading, says so with a retry, and does not wait for ever", async () => {
    stubFetch((c) => (c.url === "/api/v1/info" ? { status: 200, body: info } : { status: 200, body: settingsAnswer() }));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createMemoryRouter([{ path: "/admin/settings", element: <QueryClientProvider client={qc}><SettingsPage /></QueryClientProvider> }], {
      initialEntries: ["/admin/settings"],
    });
    render(<RouterProvider router={router} />);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Settings");
    expect((await screen.findByRole("alert")).textContent).toContain("Could not reach the server");
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Settings");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
  });
});
