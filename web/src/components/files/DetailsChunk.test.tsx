// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import Details from "./Details";

// The chunk of the comments tabs does not load: its import fails, as a network failure makes it fail.
vi.mock("../comments/CommentsTabs", () => {
  throw new Error("chunk did not load");
});

function file(): Node {
  return {
    id: "n1",
    folder_id: "f1",
    parent_id: null,
    name: "plan.txt",
    kind: "file",
    size: 2048,
    mime_type: "text/plain",
    sha256: "ab".repeat(32),
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-02T00:00:00Z",
    created_by: "u1",
    owner_id: "u2",
    role: "editor",
    no_download: false,
    editors_can_share: false,
  };
}

describe("Details when the comments chunk does not load", () => {
  beforeEach(() => {
    session.set({
      user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
  });
  afterEach(() => session.set(null));

  it("shows an error where the tabs were, and the rest of the drawer stays usable", async () => {
    // React logs the error it catches; the test checks the drawer that is left.
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const onShare = vi.fn();
    render(
      <QueryClientProvider client={createQueryClient()}>
        <Details node={file()} canVersions={false} canShare={true} onShare={onShare} onClose={vi.fn()} onVersions={vi.fn()} />
      </QueryClientProvider>,
    );
    expect((await screen.findByRole("alert")).textContent).toContain("Something went wrong. Try again.");
    expect(document.querySelector('[data-testid="details"]')?.textContent).toContain("plan.txt");
    screen.getByRole("button", { name: "Who has access" }).click();
    expect(onShare).toHaveBeenCalledTimes(1);
  });
});
