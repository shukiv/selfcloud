// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { info, signedAs } from "../../test/adminFixtures";
import InstallationPage from "./InstallationPage";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <InstallationPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("the installation page", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: info }));
    renderPage();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });

  it("an admin sees the facts and the two links", async () => {
    stubFetch((c: Call) => (c.url === "/api/v1/info" ? { status: 200, body: info } : fail(404, "not_found")));
    renderPage();
    expect(await screen.findByText("1.4.2")).toBeTruthy();
    expect(screen.getByText("https://drive.example.com")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Readiness check" }).getAttribute("href")).toBe("/readyz");
  });

  it("a failed first load shows the error and no facts (guard: failed first load)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("1.4.2")).toBeNull();
  });

  // A refetch that fails keeps the facts that were read, and says so.
  it("a failed refetch keeps the facts on screen and shows the error (guard: error shown on refetch)", async () => {
    let broken = false;
    stubFetch((c: Call) => (c.url === "/api/v1/info" ? (broken ? fail(500, "internal") : { status: 200, body: info }) : fail(404, "not_found")));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <InstallationPage />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Installation")).toBeTruthy();
    await screen.findByText(info.version);
    broken = true;
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.getByText(info.version)).toBeTruthy();
  });
});
