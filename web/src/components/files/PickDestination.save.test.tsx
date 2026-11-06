// SPDX-License-Identifier: AGPL-3.0-or-later

// The save mode of the chooser (decision 82): the "Save to" title, the "Save here" button, a place to start at (My files), and
// the name of the place chosen. A space the person can only view cannot be chosen.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FolderAccess } from "../../lib/files/types";

vi.mock("../../lib/files/api", () => ({
  childrenPage: vi.fn(async () => ({ items: [] })),
}));

import PickDestination from "./PickDestination";

const space = (id: string, name: string, role: FolderAccess["role"], kind: FolderAccess["kind"] = "team"): FolderAccess => ({
  id,
  name,
  kind,
  access: "write",
  role,
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
});

const spaces = [space("f1", "Team A", "editor"), space("f2", "Team B", "viewer"), space("f3", "My space", "manager", "personal")];

const disabled = (el: HTMLElement) => (el as HTMLButtonElement).disabled;

describe("PickDestination in save mode", () => {
  it("is titled Save to, starts at the place given, and saves there by name", () => {
    const onPick = vi.fn();
    render(<PickDestination mode="save" spaces={spaces} startAt="f3" onPick={onPick} onClose={() => {}} />);
    expect(screen.getByRole("dialog").textContent).toContain("Save to");
    const submit = screen.getByRole("button", { name: "Save here" });
    expect(disabled(submit)).toBe(false);
    fireEvent.click(submit);
    expect(onPick).toHaveBeenCalledWith({ folder: "f3", dir: null, name: "My space" });
  });

  it("starts with nothing chosen when no place is given, so Save here is off", () => {
    render(<PickDestination mode="save" spaces={spaces} onPick={() => {}} onClose={() => {}} />);
    expect(disabled(screen.getByRole("button", { name: "Save here" }))).toBe(true);
  });

  it("cannot choose a place the person can only view", () => {
    render(<PickDestination mode="save" spaces={spaces} startAt="f2" onPick={() => {}} onClose={() => {}} />);
    expect(disabled(screen.getByRole("button", { name: "Save here" }))).toBe(true);
  });
});
