// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import { fail, stubFetch } from "../../test/fetchStub";
import AddShortcut from "./AddShortcut";

function target(over: Partial<Node> = {}): Node {
  return {
    id: "nod_target",
    folder_id: "fld_1",
    parent_id: "nod_dir",
    name: "Quarterly report.pdf",
    kind: "file",
    size: 10,
    mime_type: "application/pdf",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
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

function dialog(): HTMLElement {
  return screen.getByRole("dialog");
}

describe("AddShortcut", () => {
  beforeEach(() => signIn());
  afterEach(() => {
    session.set(null);
    vi.unstubAllGlobals();
  });

  it("shows the name the shortcut will use, prefilled from the target and editable, before it is created", async () => {
    const calls = stubFetch(() => ({ status: 201, body: { id: "nod_new" } }));
    const onDone = vi.fn();
    render(<AddShortcut target={target()} dest={{ folder: "fld_1", dir: "nod_dir" }} onDone={onDone} onClose={vi.fn()} />);
    const name = screen.getByRole("textbox", { name: "Name in this folder" }) as HTMLInputElement;
    expect(name.value).toBe("Quarterly report.pdf");
    expect(calls.calls).toHaveLength(0);

    fireEvent.change(name, { target: { value: "Q3 report.pdf" } });
    fireEvent.click(screen.getByRole("button", { name: "Create shortcut" }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    const post = calls.calls.find((c) => c.method === "POST");
    expect(post?.url).toBe("/api/v1/nodes/nod_dir/shortcuts");
    expect(post?.body).toEqual({ target: "nod_target", name: "Q3 report.pdf" });
  });

  it("refuses an empty or a slashed name in the dialog, and sends nothing", () => {
    const calls = stubFetch(() => ({ status: 201, body: {} }));
    render(<AddShortcut target={target()} dest={{ folder: "fld_1", dir: "nod_dir" }} onDone={vi.fn()} onClose={vi.fn()} />);
    const name = screen.getByRole("textbox", { name: "Name in this folder" });
    fireEvent.change(name, { target: { value: "  " } });
    fireEvent.click(screen.getByRole("button", { name: "Create shortcut" }));
    expect(dialog().textContent).toContain("Enter a name.");
    fireEvent.change(name, { target: { value: "a/b" } });
    fireEvent.click(screen.getByRole("button", { name: "Create shortcut" }));
    expect(calls.calls).toHaveLength(0);
  });

  it("shows a name already taken in the dialog, and keeps the dialog open", async () => {
    stubFetch(() => fail(409, "conflict", { reason: "name" }));
    const onDone = vi.fn();
    render(<AddShortcut target={target()} dest={{ folder: "fld_1", dir: "nod_dir" }} onDone={onDone} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Create shortcut" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(onDone).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox", { name: "Name in this folder" })).toBeTruthy();
  });

  it("keeps the error clear of the name's hint, with a gap above it", async () => {
    stubFetch(() => fail(409, "conflict", { reason: "name" }));
    render(<AddShortcut target={target()} dest={{ folder: "fld_1", dir: "nod_dir" }} onDone={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Create shortcut" }));
    const wrapper = (await waitFor(() => screen.getByRole("alert"))).closest(".shortcut-error") as HTMLElement | null;
    expect(wrapper).not.toBeNull();
    expect(getComputedStyle(wrapper as HTMLElement).marginTop).toBe("12px");
  });
});
