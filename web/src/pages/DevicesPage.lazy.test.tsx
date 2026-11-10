// SPDX-License-Identifier: AGPL-3.0-or-later

// Signed-in devices' strings are a catalogue of their own (web/src/i18n/cli). This file runs the lazy path: it does not preload
// the catalogue, and its one test is the first render of the file, so nothing is loaded yet. The page must show no key and no
// English before the catalogue arrives (a spinner), then its real strings and the list.
import { render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";
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

beforeEach(() => session.set(signedAs("olivia")));

describe("Signed-in devices loads its own strings", () => {
  it("shows a spinner, no key and no English before the catalogue arrives, then its real strings and the list", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/devices") {
        return {
          status: 200,
          body: {
            devices: [
              {
                id: "dev_1",
                client_name: "laptop",
                client_ip: "",
                user_agent: "",
                created_at: "2026-11-10T08:00:00Z",
                expires_at: "2026-12-10T08:00:00Z",
              },
            ],
          },
        };
      }
      return fail(404, "not_found");
    });
    const { container } = render(
      <QueryClientProvider client={createQueryClient()}>
        <DevicesPage />
      </QueryClientProvider>,
    );
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
    expect(screen.queryByText("Signed-in devices")).toBeNull();
    expect(container.querySelector(".ant-spin")).not.toBeNull();
    expect(await screen.findByText("Signed-in devices")).toBeTruthy();
    expect(await screen.findByText("laptop")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
  });
});
