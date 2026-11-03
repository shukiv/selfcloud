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
import { SIDEBAR, type Sidebar } from "../../theme/tokens";

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
  colours,
}: {
  folder?: string;
  chain?: string[];
  start?: TreeState;
  coarse?: boolean;
  top?: { id: string; name: string; role: string | undefined };
  colours?: Sidebar;
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
        colours={colours}
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

  // Decision 69: the tree starts folded. A place deep in the tree does not open the folders on the way; the nearest row on screen
  // (here, the space's own row) is the one marked.
  it("starts folded: the folders on the way to the place are not opened, and the nearest row on screen is marked", () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBe("location");
    expect(screen.queryByText("Docs")).toBeNull();
    expect(latest.expanded).toEqual([]);
  });

  // Decision 69, review fix 8: the place changes after the tree is mounted: no folder on its way opens, and a row is marked only
  // when it is on screen.
  it("a place changed after mount opens none of the folders on its way, and marks nothing when none of the way is on screen", async () => {
    const { rerender } = render(<Harness folder="fld_a" />);
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBe("location");
    rerender(<Harness folder="fld_deep" chain={["nod_one", "nod_deep"]} />);
    // The space row is not on the way (only the folders below its root are), and nothing on the way is shown: nothing is marked.
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBeNull();
    expect(document.querySelector('[aria-current="location"]')).toBeNull();
    expect(screen.queryByRole("treeitem", { name: "Deep" })).toBeNull();
    expect(screen.queryByText("Docs")).toBeNull();
    expect(latest.expanded).toEqual([]);
    expect(api.childrenPage).not.toHaveBeenCalled();
  });

  it("marks the deepest folder on the way that is on screen, once the branches above it are open", async () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} start={{ expanded: ["fld_a", "nod_one"] }} />);
    const deep = await screen.findByRole("treeitem", { name: "Deep" });
    expect(deep.getAttribute("aria-current")).toBe("location");
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBeNull();
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBeNull();
  });

  it("does not open a folder again that the person closed, while the place stays the same", async () => {
    render(<Harness folder="fld_a" chain={["nod_one"]} start={{ expanded: ["fld_a"] }} />);
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
    // The space's own root is not in the tree, and the given folder is a row, folded (decision 69), and it is the mark: the
    // nearest row on screen. Nothing under it is read until the person opens it.
    expect(api.childrenPage).not.toHaveBeenCalledWith("fld_a", null, expect.anything(), expect.anything());
    expect(api.childrenPage).not.toHaveBeenCalled();
    expect(screen.queryByText("Team A")).toBeNull();
    const top = screen.getByText("Open").closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(top.parentElement?.getAttribute("aria-current")).toBe("location");
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

// Decision 69 (option B): the current folder has no background. It has a 3 px bar at its start edge and its text and icon in the
// sidebar's mark colour (the yellow on the default blue). Other rows keep the sidebar's text. Its weight is the menu's.
describe("the folder tree in the sidebar: the current row's mark", () => {
  const rowOf = (name: string) => screen.getByText(name).closest<HTMLElement>(".tree-row");

  it("fills a top-level current place with the sidebar's active colour and its on-active text (decision 72); the sidebar's text on the others", () => {
    render(<Harness colours={SIDEBAR} folder="fld_a" start={{ expanded: ["fld_a"] }} />);
    const current = rowOf("Team A");
    expect(current?.style.background).toBe("rgb(254, 204, 0)");
    expect(current?.style.color).toBe("rgb(13, 33, 66)");
    expect(current?.classList.contains("side-current-full")).toBe(true);
    expect(current?.classList.contains("side-current")).toBe(false);
    const other = rowOf("My files");
    expect(other?.style.color).toBe("rgb(255, 255, 255)");
    expect(other?.style.background).toBe("");
    expect(other?.classList.contains("side-current")).toBe(false);
  });

  it("gives the arrow of the filled top-level row the on-active colour too", async () => {
    render(<Harness colours={SIDEBAR} folder="fld_a" start={{ expanded: ["fld_a"] }} />);
    const arrow = await screen.findByRole("button", { name: "Collapse Team A" });
    expect(arrow.style.color).toBe("rgb(13, 33, 66)");
  });

  // Decision 69: the current row is not bold, as the menu's selected item is not (the sidebar's rows look alike in every state).
  it("is not bold on the current row, as the menu's selected item is not", () => {
    render(<Harness colours={SIDEBAR} folder="fld_a" start={{ expanded: ["fld_a"] }} />);
    expect(rowOf("Team A")?.style.fontWeight).toBe("");
  });

  // Decision 69: the place is not in the tree (its branch is folded), so the mark sits on the nearest row on screen.
  it("puts the mark on the nearest row on screen when the place is folded away", () => {
    render(<Harness colours={SIDEBAR} folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    const current = rowOf("Team A");
    expect(current?.classList.contains("side-current-full")).toBe(true);
    expect(current?.style.background).toBe("rgb(254, 204, 0)");
  });

  // Decision 72: a folder below a top-level place keeps the bar, with no background; the place above is not marked.
  it("marks a deeper current folder with the bar and no fill, and leaves its top-level row plain", async () => {
    render(<Harness colours={SIDEBAR} folder="nod_one" chain={["nod_one"]} start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    const deep = rowOf("Docs");
    expect(deep?.classList.contains("side-current")).toBe(true);
    expect(deep?.style.background).toBe("");
    expect(deep?.style.color).toBe("rgb(254, 204, 0)");
    const top = rowOf("Team A");
    expect(top?.classList.contains("side-current-full")).toBe(false);
    expect(top?.style.background).toBe("");
  });
});
