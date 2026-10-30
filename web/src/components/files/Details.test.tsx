// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import Details from "./Details";

function file(over: Partial<Node> = {}): Node {
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
    ...over,
  };
}

function signIn(id: string) {
  session.set({
    user: { id, email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  });
}

describe("Details", () => {
  beforeEach(() => signIn("u1"));
  afterEach(() => session.set(null));

  it("shows the signed-in person's name for their own ID, and never a raw ID", () => {
    render(<Details node={file()} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    const drawer = document.querySelector('[data-testid="details"]') as HTMLElement;
    // created_by is the signed-in ID, shown as the name; owner_id is someone else's, which the API gives no name for, so its row is left out.
    expect(drawer.textContent).toContain("Olivia");
    expect(drawer.textContent).not.toContain("u1");
    expect(drawer.textContent).not.toContain("u2");
    expect(screen.queryByText("Owner")).toBeNull();
  });

  it("shows the display name the API gives for another person, and never their ID", () => {
    render(<Details node={file({ created_by: "u3", owner_id: "u3", created_by_name: "Ann Example", owner_name: "Ann Example" })} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    const drawer = document.querySelector('[data-testid="details"]') as HTMLElement;
    expect(drawer.textContent).toContain("Ann Example");
    expect(drawer.textContent).not.toContain("u3");
  });

  it("leaves out another person's row when the API gives no name, and never shows the ID", () => {
    render(<Details node={file({ created_by: "u3", owner_id: "u4" })} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    const drawer = document.querySelector('[data-testid="details"]') as HTMLElement;
    expect(drawer.textContent).not.toContain("u3");
    expect(drawer.textContent).not.toContain("u4");
    expect(screen.queryByText("Created by")).toBeNull();
    expect(screen.queryByText("Owner")).toBeNull();
  });

  it("shows Account key for a key's ID, not the key itself", () => {
    render(<Details node={file({ created_by: "key_aaaaaaaa", owner_id: undefined })} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    const drawer = document.querySelector('[data-testid="details"]') as HTMLElement;
    expect(drawer.textContent).toContain("Account key");
    expect(drawer.textContent).not.toContain("key_aaaaaaaa");
  });

  it("shows Who has access to a caller who may not share, and no Share button", () => {
    const onShare = vi.fn();
    render(<Details node={file()} canVersions={true} canShare={false} onShare={onShare} onClose={vi.fn()} onVersions={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
    screen.getByRole("button", { name: "Who has access" }).click();
    expect(onShare).toHaveBeenCalledTimes(1);
  });

  it("shows Share and Who has access to a caller who may share, and both open the share dialog", () => {
    const onShare = vi.fn();
    render(<Details node={file()} canVersions={true} canShare={true} onShare={onShare} onClose={vi.fn()} onVersions={vi.fn()} />);
    screen.getByRole("button", { name: "Share" }).click();
    screen.getByRole("button", { name: "Who has access" }).click();
    expect(onShare).toHaveBeenCalledTimes(2);
  });

  it("offers Versions for a file the caller may see versions of, and not for a folder", () => {
    const onVersions = vi.fn();
    const { unmount } = render(<Details node={file()} canVersions={true} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={onVersions} />);
    screen.getByRole("button", { name: "Versions" }).click();
    expect(onVersions).toHaveBeenCalledTimes(1);
    unmount();
    render(<Details node={file({ kind: "dir", sha256: undefined })} canVersions={true} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={onVersions} />);
    expect(screen.queryByRole("button", { name: "Versions" })).toBeNull();
  });
});

describe("Details mask", () => {
  it("a click outside the drawer does not close it", () => {
    const onClose = vi.fn();
    render(<Details node={file()} canVersions={false} canShare={false} onShare={vi.fn()} onClose={onClose} onVersions={vi.fn()} />);
    const mask = document.querySelector(".ant-drawer-mask") as HTMLElement;
    fireEvent.click(mask);
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("Details close control", () => {
  it("has one close control, the drawer's X", () => {
    render(<Details node={file()} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    expect(screen.getAllByRole("button", { name: /close/i })).toHaveLength(1);
  });
});

describe("Details versions by role", () => {
  it("offers no Versions to a caller who may not see them", () => {
    render(<Details node={file()} canVersions={false} canShare={false} onShare={vi.fn()} onClose={vi.fn()} onVersions={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Versions" })).toBeNull();
  });
});
