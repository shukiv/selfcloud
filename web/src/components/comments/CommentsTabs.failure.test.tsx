// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch } from "../../test/fetchStub";
import CommentsTabs from "./CommentsTabs";

// The comments catalogue did not load. Its texts are then not there, so the section says what happened in the main texts.
vi.mock("../../lib/comments/i18n", async (orig) => ({
  ...(await orig<typeof import("../../lib/comments/i18n")>()),
  useCT: () => ({ ready: false, failed: true, t: (key: string) => key }),
}));

describe("the comments section when its texts did not load", () => {
  beforeEach(() => {
    session.set({
      user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en", status: "active" },
      account: { id: "acc_1", name: "Acme" },
      csrf: "csrf",
      publicLinks: false,
    } as unknown as Session);
    stubFetch(() => fail(500, "internal"));
  });
  afterEach(() => {
    session.set(null);
    vi.unstubAllGlobals();
  });

  it("shows an error with a way to reload, in the main texts, and no panel", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(
      <QueryClientProvider client={qc}>
        <CommentsTabs nodeID="nod_1" role="viewer" />
      </QueryClientProvider>,
    );
    expect((await screen.findByRole("alert")).textContent).toContain("Something went wrong. Try again.");
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
    expect(container.querySelector("textarea")).toBeNull();
  });
});
