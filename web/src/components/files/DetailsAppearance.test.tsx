// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import { stubFetch } from "../../test/fetchStub";
import Details from "./Details";

// render wraps the drawer in a query client, which its label section needs, as the app does.
function render(ui: ReactElement) {
  return rtlRender(<QueryClientProvider client={createQueryClient()}>{ui}</QueryClientProvider>);
}

function dir(over: Partial<Node> = {}): Node {
  return {
    id: "nod_dir",
    folder_id: "fld_1",
    parent_id: "nod_root",
    name: "Projects",
    kind: "dir",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

function signIn() {
  session.set({
    user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  });
}

function drawer(): HTMLElement {
  return document.querySelector('[data-testid="details"]') as HTMLElement;
}

function open(node: Node, onChanged = vi.fn()) {
  render(
    <Details
      node={node}
      canVersions={false}
      canShare={false}
      onShare={vi.fn()}
      onClose={vi.fn()}
      onVersions={vi.fn()}
      onChanged={onChanged}
    />,
  );
  return onChanged;
}

describe("Details: colour and description", () => {
  beforeEach(() => signIn());
  afterEach(() => {
    session.set(null);
    vi.unstubAllGlobals();
  });

  it("shows a description as plain text, keeps its line breaks, and never renders HTML", () => {
    open(dir({ role: "viewer", description: "First line\n<b>not bold</b>\nThird" }));
    const text = drawer().querySelector('[data-testid="description"]') as HTMLElement;
    expect(text.textContent).toBe("First line\n<b>not bold</b>\nThird");
    expect(text.querySelector("b")).toBeNull();
    expect(text.getAttribute("dir")).toBe("auto");
    expect(getComputedStyle(text).whiteSpace).toBe("pre-wrap");
  });

  it("gives an editor a colour to pick and a description to change", () => {
    open(dir({ color: "red" }));
    const colours = screen.getByRole("radiogroup", { name: "Colour" });
    expect(within(colours).getByRole("radio", { name: "Red" })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Description" })).toBeTruthy();
  });

  it("saves a colour and a description with one PATCH, then tells the page", async () => {
    const calls = stubFetch(() => ({ status: 200, body: dir({ color: "teal", description: "Notes" }) }));
    const onChanged = open(dir());
    fireEvent.click(screen.getByRole("radio", { name: "Teal" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), { target: { value: "Notes" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    const patch = calls.calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toBe("/api/v1/nodes/nod_dir");
    expect(patch?.body).toEqual({ color: "teal", description: "Notes" });
  });

  it("keeps the editor's controls after a save whose answer carries no role", async () => {
    // The PATCH answer is a node without the caller's role, so the drawer must keep the role it already had.
    stubFetch(() => ({ status: 200, body: { id: "nod_dir", folder_id: "fld_1", parent_id: "nod_root", name: "Projects", kind: "dir", size: 0, color: "teal", description: "Notes", created_at: "2026-10-01T00:00:00Z", modified_at: "2026-10-02T00:00:00Z", no_download: false, editors_can_share: false } }));
    const onChanged = open(dir());
    fireEvent.click(screen.getByRole("radio", { name: "Teal" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(onChanged.mock.calls[0][0].role).toBe("editor");
    expect(await screen.findByRole("status")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
  });

  it("gives a reader no colour or description control, only what is set", () => {
    open(dir({ role: "viewer", color: "green", description: "Read me" }));
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(drawer().textContent).toContain("Read me");
    expect(drawer().textContent).toContain("Green");
  });
});
