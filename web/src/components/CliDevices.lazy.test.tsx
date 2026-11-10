// SPDX-License-Identifier: AGPL-3.0-or-later

// CliDevices reads its own strings (web/src/i18n/cli). DevicesPage waits for them too, so in the app the list never shows a key;
// this file checks the list on its own, which is the rule the component keeps for itself. It runs the lazy path: it does not
// preload the catalogue, and its one test is the first render of the file.
import { render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import CliDevices from "./CliDevices";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedAs("olivia")));

describe("the device list loads its own strings", () => {
  it("shows a spinner, no key and no English before the catalogue arrives, then the list's real strings", async () => {
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
        <CliDevices />
      </QueryClientProvider>,
    );
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
    expect(container.querySelector(".ant-spin")).not.toBeNull();
    expect(screen.queryByText("Name")).toBeNull();
    expect(await screen.findByText("laptop")).toBeTruthy();
    expect(screen.getByText("Name")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
  });
});
