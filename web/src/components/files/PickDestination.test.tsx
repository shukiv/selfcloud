// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FolderAccess, Node, Page } from "../../lib/files/types";

const childrenPage = vi.fn();
vi.mock("../../lib/files/api", () => ({
  childrenPage: (...args: unknown[]) => childrenPage(...args),
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

function dir(id: string, name: string, role: Node["role"]): Node {
  return {
    id,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "dir",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
  };
}

// Team A is a space the caller may edit; Team B is view only; My files is personal and editable.
const spaces = [space("f1", "Team A", "editor"), space("f2", "Team B", "viewer"), space("f3", "My space", "manager", "personal")];

// One listing per place: Team A has Plans (editable) and Archive (view only); Plans has Q1 below it.
function listings(folder: string, dirID: string | null): Page<Node> {
  if (folder === "f1" && dirID === null) return { items: [dir("d1", "Plans", "editor"), dir("d2", "Archive", "viewer")] };
  if (folder === "f1" && dirID === "d1") return { items: [dir("d3", "Q1", "contributor")] };
  if (folder === "f2" && dirID === null) return { items: [dir("d9", "Shared docs", "viewer")] };
  return { items: [] };
}

describe("PickDestination", () => {
  beforeEach(() => {
    childrenPage.mockReset();
    childrenPage.mockImplementation(async (folder: string, dir: string | null) => listings(folder, dir));
  });

  it("lists the spaces, with My files for the personal one", () => {
    render(<PickDestination mode="move" spaces={spaces} onPick={() => {}} onClose={() => {}} />);
    expect(screen.getByText("Team A")).toBeTruthy();
    expect(screen.getByText("Team B")).toBeTruthy();
    expect(screen.getByText("My files")).toBeTruthy();
    expect(childrenPage).not.toHaveBeenCalled();
  });

  it("opens a space lazily and drills into its directories", async () => {
    render(<PickDestination mode="copy" spaces={spaces} onPick={() => {}} onClose={() => {}} />);
    const row = screen.getByText("Team A").closest(".ant-tree-treenode") as HTMLElement;
    fireEvent.click(row.querySelector(".ant-tree-switcher") as Element);
    expect(await screen.findByText("Plans")).toBeTruthy();
    expect(childrenPage).toHaveBeenCalledWith("f1", null, undefined);
    const plans = screen.getByText("Plans").closest(".ant-tree-treenode") as HTMLElement;
    fireEvent.click(plans.querySelector(".ant-tree-switcher") as Element);
    expect(await screen.findByText("Q1")).toBeTruthy();
    expect(childrenPage).toHaveBeenCalledWith("f1", "d1", undefined);
  });

  it("refuses a view-only place: submit stays off and the note says why", async () => {
    render(<PickDestination mode="move" spaces={spaces} onPick={() => {}} onClose={() => {}} />);
    fireEvent.click(screen.getByText("Team B"));
    expect(await screen.findByText(/only view this place/)).toBeTruthy();
    const submit = screen.getByRole("button", { name: "Move here" });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
  });

  it("submits the chosen place with its folder and directory", async () => {
    const onPick = vi.fn();
    render(<PickDestination mode="move" spaces={spaces} onPick={onPick} onClose={() => {}} />);
    fireEvent.click(screen.getByText("My space"));
    const submit = screen.getByRole("button", { name: "Move here" });
    await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(submit);
    expect(onPick).toHaveBeenLastCalledWith({ folder: "f3", dir: null });
  });

  it("submits a directory by its ID, and the copy title is used in copy mode", async () => {
    const onPick = vi.fn();
    render(<PickDestination mode="copy" spaces={spaces} onPick={onPick} onClose={() => {}} />);
    expect(screen.getByRole("dialog").textContent).toContain("Copy to…");
    fireEvent.click(screen.getByText("Team A"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Copy here" }).hasAttribute("disabled")).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Copy here" }));
    expect(onPick).toHaveBeenLastCalledWith({ folder: "f1", dir: null });
  });
});

describe("PickDestination mask", () => {
  it("a click outside the dialog does not close it", () => {
    const onClose = vi.fn();
    childrenPage.mockImplementation(async () => ({ items: [] }));
    render(<PickDestination mode="move" spaces={spaces} onPick={() => {}} onClose={onClose} />);
    const mask = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.mouseDown(mask);
    fireEvent.mouseUp(mask);
    fireEvent.click(mask);
    expect(onClose).not.toHaveBeenCalled();
  });
});
