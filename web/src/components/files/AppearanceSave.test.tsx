// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import { stubFetch } from "../../test/fetchStub";
import Appearance from "./Appearance";

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

describe("Appearance saves", () => {
  beforeEach(() => {
    session.set({
      user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
  });
  afterEach(() => {
    session.set(null);
    vi.unstubAllGlobals();
  });

  it("reports None as no colour, when the server's answer leaves the colour out", async () => {
    // An empty colour is left out of the answer, so the report must say the colour is gone.
    stubFetch(() => ({ status: 200, body: dir({ color: undefined }) }));
    const onChanged = vi.fn();
    render(<Appearance node={dir({ color: "teal" })} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("radio", { name: "None" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(onChanged.mock.calls[0][0].color).toBeUndefined();
  });

  it("reports nothing for a save whose drawer has closed before the answer", async () => {
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    stubFetch(async () => {
      await gate;
      return { status: 200, body: dir({ color: "red" }) };
    });
    const onChanged = vi.fn();
    const { unmount } = render(<Appearance node={dir()} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("radio", { name: "Red" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    unmount();
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("clears the Saved note when the person edits again", async () => {
    stubFetch(() => ({ status: 200, body: dir({ description: "Notes" }) }));
    render(<Appearance node={dir()} onChanged={vi.fn()} />);
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), { target: { value: "Notes" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("Saved.");
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), { target: { value: "Notes again" } });
    expect(screen.queryByText("Saved.")).toBeNull();
  });
});
