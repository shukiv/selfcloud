// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { session, type Session } from "../../lib/session";
import type { FolderAccess, Node } from "../../lib/files/types";
import { DEFAULT_TREE, type TreeState } from "../../lib/files/treeState";

const api = vi.hoisted(() => ({ childrenPage: vi.fn() }));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), ...api }));

import FolderTree from "./FolderTree";

const space = (id: string, name: string, extra: Partial<FolderAccess> = {}): FolderAccess => ({
  id,
  name,
  kind: "team",
  access: "write",
  role: "editor",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
  ...extra,
});
const dir = (id: string, name: string, role: Node["role"] = "editor"): Node => ({
  id,
  folder_id: "fld_a",
  parent_id: null,
  name,
  kind: "dir",
  size: 0,
  created_at: "",
  modified_at: "",
  role,
  no_download: false,
  editors_can_share: false,
});

let latest: TreeState = DEFAULT_TREE;
const onOpen = vi.fn();

// Harness keeps the state the way the page does: it is the parent's, and changes come back through onChange.
function Harness({
  folder = "fld_a",
  chain = [] as string[],
  start = DEFAULT_TREE,
  coarse = false,
  top,
}: {
  folder?: string;
  chain?: string[];
  start?: TreeState;
  coarse?: boolean;
  top?: { id: string; name: string; role: string | undefined };
}) {
  const [state, setState] = useState(start);
  latest = state;
  return (
    <QueryClientProvider client={createQueryClient()}>
      <FolderTree
        spaces={[space("fld_a", "Team A"), space("fld_me", "Mine", { kind: "personal" })]}
        folder={folder}
        marking
        chain={chain}
        state={state}
        onChange={(n) => {
          latest = n;
          setState(n);
        }}
        onOpen={onOpen}
        coarse={coarse}
        top={top}
      />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  latest = DEFAULT_TREE;
  session.set({ user: { id: "u1", email: "a@example.com", display_name: "A", role: "member", locale: "en", status: "active" }, account: null, csrf: "t", publicLinks: false } as Session);
  api.childrenPage.mockImplementation(async (folder: string, d: string | null) => {
    if (folder === "fld_a" && d === null) return { items: [dir("nod_one", "Docs"), dir("nod_two", "Pics", "viewer")], next_cursor: undefined };
    if (d === "nod_one") return { items: [dir("nod_deep", "Deep")], next_cursor: undefined };
    return { items: [], next_cursor: undefined };
  });
});
afterEach(() => session.set(null));

describe("the folder tree", () => {
  it("lists the spaces, with the personal one called My files, and reads nothing until one is opened", () => {
    render(<Harness folder="fld_zz" />);
    expect(screen.getByRole("tree", { name: "Folders" })).toBeTruthy();
    expect(screen.getByText("Team A")).toBeTruthy();
    expect(screen.getByText("My files")).toBeTruthy();
    expect(api.childrenPage).not.toHaveBeenCalled();
  });

  it("opens a space on its arrow, reads its folders once, sorted by name, and keeps the opened state", async () => {
    render(<Harness folder="fld_zz" />);
    fireEvent.click(screen.getByRole("button", { name: "Expand Team A" }));
    expect(await screen.findByText("Docs")).toBeTruthy();
    expect(screen.getByText("Pics")).toBeTruthy();
    expect(api.childrenPage).toHaveBeenCalledTimes(1);
    expect(api.childrenPage).toHaveBeenCalledWith("fld_a", null, undefined, { key: "name", dir: "asc" });
    expect(latest.expanded).toContain("fld_a");
    // A second level opens the same way, and closing keeps nothing read twice.
    fireEvent.click(screen.getByRole("button", { name: "Expand Docs" }));
    expect(await screen.findByText("Deep")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Collapse Team A" }));
    expect(screen.queryByText("Docs")).toBeNull();
    expect(latest.expanded).toContain("nod_one");
    expect(latest.expanded).not.toContain("fld_a");
  });

  it("opens a folder in the listing when its name is clicked, and does not open it for the arrow", async () => {
    render(<Harness start={{ expanded: ["fld_a"] }} />);
    fireEvent.click(await screen.findByText("Docs"));
    expect(onOpen).toHaveBeenCalledWith("fld_a", "nod_one");
    onOpen.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "Expand Docs" }));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("opens the folders on the way to the place shown, and marks the place", async () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    const deep = await screen.findByText("Deep");
    expect(deep.closest('[role="treeitem"]')?.getAttribute("aria-current")).toBe("location");
    expect(latest.expanded).toEqual(["fld_a", "nod_one", "nod_deep"]);
  });

  it("does not open a folder again that the person closed, while the place stays the same", async () => {
    render(<Harness folder="fld_a" chain={["nod_one"]} />);
    await screen.findByText("Docs");
    fireEvent.click(screen.getByRole("button", { name: "Collapse Team A" }));
    await waitFor(() => expect(screen.queryByText("Docs")).toBeNull());
    expect(latest.expanded).not.toContain("fld_a");
  });

  it("makes every folder a place to drop, with the role there and the ids on the way down", async () => {
    render(<Harness start={{ expanded: ["fld_a", "nod_one"] }} />);
    const deep = (await screen.findByText("Deep")).closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(deep.dataset.dropFolder).toBe("fld_a");
    expect(deep.dataset.dropDir).toBe("nod_deep");
    expect(deep.dataset.dropPath).toBe("fld_a,nod_one,nod_deep");
    const pics = screen.getByText("Pics").closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(pics.dataset.dropRole).toBe("viewer");
    const root = screen.getByText("Team A").closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(root.dataset.dropDir).toBe("");
    expect(root.dataset.dropRole).toBe("editor");
  });

  it("shows an error with a Retry when a folder cannot be read, and reads again on Retry", async () => {
    api.childrenPage.mockRejectedValueOnce(new ApiError(500, "internal", {}, 0)).mockRejectedValueOnce(new ApiError(500, "internal", {}, 0));
    render(<Harness start={{ expanded: ["fld_a"] }} />);
    const alert = await screen.findByRole("alert", undefined, { timeout: 8000 });
    expect(within(alert).getByText(/could not be loaded/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Docs")).toBeTruthy();
  });

  it("makes the arrow and the link 44 px on a finger, and smaller with a mouse", async () => {
    const { unmount } = render(<Harness coarse start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    const arrow = screen.getByRole("button", { name: "Collapse Team A" });
    expect(arrow.getAttribute("style")).toContain("min-block-size: 44px");
    expect(arrow.getAttribute("style")).toContain("min-inline-size: 44px");
    const link = screen.getByText("Docs").closest("a") as HTMLElement;
    expect(link.getAttribute("style")).toContain("min-block-size: 44px");
    unmount();
    render(<Harness start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    expect((screen.getByRole("button", { name: "Collapse Team A" }).getAttribute("style") ?? "")).toContain("min-block-size: 24px");
  });

  it("shows no failure and no Retry for a space whose root the server says is not there for this person", async () => {
    api.childrenPage.mockRejectedValue(new ApiError(404, "not_found", {}, 0));
    render(<Harness folder="fld_zz" start={{ expanded: ["fld_a"] }} />);
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    // The space has nothing to open once the server has said so.
    await waitFor(() => expect(screen.queryByRole("button", { name: /Collapse Team A|Expand Team A/ })).toBeNull());
  });

  it("starts at the folder the person was given when the space's root is not theirs, and reads from there", async () => {
    render(<Harness folder="fld_a" chain={["nod_top", "nod_below"]} top={{ id: "nod_top", name: "Open", role: "viewer" }} />);
    // The space's own root is not in the tree, and the given folder is, opened, with the place marked.
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_a", "nod_top", undefined, { key: "name", dir: "asc" }));
    expect(api.childrenPage).not.toHaveBeenCalledWith("fld_a", null, expect.anything(), expect.anything());
    expect(screen.queryByText("Team A")).toBeNull();
    const top = screen.getByText("Open").closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(top.dataset.dropDir).toBe("nod_top");
    expect(top.dataset.dropPath).toBe("fld_a,nod_top");
    expect(top.dataset.dropRole).toBe("viewer");
    expect(screen.getByText("My files")).toBeTruthy();
  });

  it("moves with the arrow keys, opens with Enter, and opens and closes with Right and Left", async () => {
    render(<Harness start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    const items = () => screen.getAllByRole("treeitem");
    items()[0].focus();
    fireEvent.keyDown(items()[0], { key: "ArrowDown" });
    expect(document.activeElement).toBe(items()[1]);
    fireEvent.keyDown(document.activeElement as Element, { key: "Enter" });
    expect(onOpen).toHaveBeenCalledWith("fld_a", "nod_one");
    fireEvent.keyDown(document.activeElement as Element, { key: "ArrowRight" });
    await screen.findByText("Deep");
    fireEvent.keyDown(document.activeElement as Element, { key: "ArrowLeft" });
    await waitFor(() => expect(screen.queryByText("Deep")).toBeNull());
    fireEvent.keyDown(document.activeElement as Element, { key: "ArrowLeft" });
    expect((document.activeElement as HTMLElement).dataset.treeKey).toBe("fld_a");
  });

  // The one tab stop is always on a row that is rendered: the place's row when it is shown, else the first row.
  it("keeps one tab stop on a rendered row when the place is not in the tree", () => {
    render(<Harness folder="fld_zz" chain={["nod_gone"]} />);
    const stops = Array.from(document.querySelectorAll<HTMLElement>('[role="treeitem"]')).filter((x) => x.tabIndex === 0);
    expect(stops).toHaveLength(1);
    expect(stops[0].dataset.treeKey).toBe("fld_a");
  });

  // A row is named by its own folder, not by the folders and arrows under it.
  it("names each treeitem by its own folder, not by the folders and arrows under it", async () => {
    render(<Harness start={{ expanded: ["fld_a", "nod_one"] }} />);
    expect(screen.getByRole("treeitem", { name: "Team A" }).dataset.treeKey).toBe("fld_a");
    expect((await screen.findByRole("treeitem", { name: "Docs" })).dataset.treeKey).toBe("nod_one");
  });

  // A file dragged from the desktop onto the tree does not navigate the browser away from the app.
  it("does not let a file dropped on the tree leave the page", () => {
    render(<Harness />);
    const dt = { types: ["Files"], files: [new File(["x"], "d.txt")], items: [], dropEffect: "none" };
    const kept = fireEvent.drop(screen.getByTestId("folder-tree"), { dataTransfer: dt });
    expect(kept).toBe(false);
  });
});
