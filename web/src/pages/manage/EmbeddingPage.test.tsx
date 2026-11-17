// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { expectEmptyTable } from "../../test/emptyTable";
import { DESKTOP, setViewport } from "../../test/viewport";
import EmbeddingPage from "./EmbeddingPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const EMBED = "/api/v1/accounts/acc_1/embed";
const isGet = (c: Call) => c.method === "GET" && c.url === EMBED;
const isPut = (c: Call) => c.method === "PUT" && c.url === EMBED;

function renderPage() {
  const qc = createQueryClient();
  render(
    <App>
      <QueryClientProvider client={qc}>
        <EmbeddingPage />
      </QueryClientProvider>
    </App>,
  );
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

describe("embedding page", () => {
  it("lists the account's origins from the read, each left to right", async () => {
    stubFetch((c) => (isGet(c) ? { status: 200, body: { origins: ["https://a.example", "https://b.example"] } } : fail(404, "not_found")));
    renderPage();
    const region = await screen.findByRole("region", { name: "Embedding" });
    expect(await within(region).findByText("https://a.example")).toBeTruthy();
    expect(within(region).getByText("https://b.example")).toBeTruthy();
    // An origin reads left to right even in a right-to-left language.
    expect(within(region).getByText("https://a.example").getAttribute("dir")).toBe("ltr");
    expect(screen.getByText("Sites listed here may show this account's pages inside their own")).toBeTruthy();
  });

  it("an empty list keeps its table and headers, with the title, description and Add site in the body", async () => {
    stubFetch((c) => (isGet(c) ? { status: 200, body: { origins: [] } } : fail(404, "not_found")));
    renderPage();
    await expectEmptyTable("Embedding", {
      headers: ["Site", "Actions"],
      title: "No sites yet",
      description: "Add the address of a site that will show the drive in a frame",
      action: "Add site",
    });
  });

  it("Add sends the old list plus the new origin, closes on 200 and shows the answer's list", async () => {
    const { calls } = stubFetch((c) => {
      if (isPut(c)) return { status: 200, body: { origins: ["https://a.example", "https://new.example"] } };
      return { status: 200, body: { origins: ["https://a.example"] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Embedding" });
    await within(region).findByText("https://a.example");
    fireEvent.click(screen.getByRole("button", { name: "Add site" }));
    const input = await screen.findByPlaceholderText("https://app.example.com");
    // Add is disabled while the input is empty.
    expect((screen.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(input, { target: { value: "https://new.example" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(calls.filter(isPut)).toHaveLength(1));
    expect(calls.find(isPut)?.body).toEqual({ origins: ["https://a.example", "https://new.example"] });
    // The dialog closes and the server's normalised list is what shows.
    await waitFor(() => expect(screen.queryByPlaceholderText("https://app.example.com")).toBeNull());
    expect(await within(region).findByText("https://new.example")).toBeTruthy();
    expect(within(region).getByText("https://a.example")).toBeTruthy();
  });

  it("a 400 keeps the dialog open and shows the server's message under the input", async () => {
    const { calls } = stubFetch((c) => {
      if (isPut(c)) return { status: 400, body: { error: { code: "invalid_request", message: 'origin "not an origin" is not a valid origin' } } };
      return { status: 200, body: { origins: [] } };
    });
    renderPage();
    await screen.findByRole("region", { name: "Embedding" });
    fireEvent.click(screen.getAllByRole("button", { name: "Add site" })[0]);
    const input = await screen.findByPlaceholderText("https://app.example.com");
    fireEvent.change(input, { target: { value: "not an origin" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByText('origin "not an origin" is not a valid origin')).toBeTruthy();
    // The dialog stays open, the input keeps what was typed, and nothing was written twice.
    expect(screen.getByPlaceholderText("https://app.example.com")).toBeTruthy();
    expect(calls.filter(isPut)).toHaveLength(1);
  });

  it("Remove asks first, then sends the list without that origin", async () => {
    const { calls } = stubFetch((c) => {
      if (isPut(c)) return { status: 200, body: { origins: ["https://b.example"] } };
      return { status: 200, body: { origins: ["https://a.example", "https://b.example"] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Embedding" });
    await within(region).findByText("https://a.example");
    fireEvent.click(within(region).getByRole("button", { name: "Remove: https://a.example" }));
    // The question names the origin and what stops working.
    expect(await screen.findByText("Remove https://a.example? Pages framed by it stop working at their next load")).toBeTruthy();
    expect(calls.filter(isPut)).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(calls.filter(isPut)).toHaveLength(1));
    expect(calls.find(isPut)?.body).toEqual({ origins: ["https://b.example"] });
    await waitFor(() => expect(within(region).queryByText("https://a.example")).toBeNull());
    expect(within(region).getByText("https://b.example")).toBeTruthy();
  });

  it("while a PUT runs the table's actions are disabled", async () => {
    stubFetch((c) => {
      if (isPut(c)) return new Promise<Answer>(() => undefined);
      return { status: 200, body: { origins: ["https://a.example"] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Embedding" });
    await within(region).findByText("https://a.example");
    fireEvent.click(within(region).getByRole("button", { name: "Remove: https://a.example" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    await waitFor(() => expect((within(region).getByRole("button", { name: "Remove: https://a.example" }) as HTMLButtonElement).disabled).toBe(true));
    expect((screen.getByRole("button", { name: "Add site" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("a failed PUT keeps the list as it was and shows the error", async () => {
    stubFetch((c) => {
      if (isPut(c)) return fail(500, "internal");
      return { status: 200, body: { origins: ["https://a.example"] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Embedding" });
    await within(region).findByText("https://a.example");
    fireEvent.click(within(region).getByRole("button", { name: "Remove: https://a.example" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    // The origin is still listed: a failed write changes nothing on screen.
    expect(within(region).getByText("https://a.example")).toBeTruthy();
  });

  it("a failed first load shows the error with a Retry that reads again", async () => {
    let failLoad = true;
    const { calls } = stubFetch((c) => {
      if (isGet(c)) return failLoad ? fail(500, "internal") : { status: 200, body: { origins: ["https://a.example"] } };
      return fail(404, "not_found");
    });
    renderPage();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeTruthy();
    expect(screen.queryByText("No sites yet")).toBeNull();
    failLoad = false;
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    const region = await screen.findByRole("region", { name: "Embedding" });
    expect(await within(region).findByText("https://a.example")).toBeTruthy();
    expect(calls.filter(isGet).length).toBeGreaterThanOrEqual(2);
  });
});
