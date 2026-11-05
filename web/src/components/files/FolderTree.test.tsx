// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient, queryKey } from "../../lib/query";
import { resetUserStores } from "../../lib/store";
import { ApiError } from "../../lib/apiError";
import { session, type Session } from "../../lib/session";
import type { FolderAccess, Node } from "../../lib/files/types";
import { DEFAULT_TREE, type TreeState } from "../../lib/files/treeState";
import { SIDEBAR, type Sidebar } from "../../theme/tokens";
import { sidebarRow } from "../../theme/sidebarRow";
import { theme as antdTheme } from "antd";

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
// changes counts the states the tree sends its parent: a loop shows as a count that does not stop.
let changes = 0;
// client is the query client of the mounted harness, for tests that refetch a folder.
let client: ReturnType<typeof createQueryClient> | null = null;
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
  // One client for the whole mount, as the app has: a rerender with a new place must not forget what was read.
  const [qc] = useState(createQueryClient);
  client = qc;
  latest = state;
  return (
    <QueryClientProvider client={qc}>
      <FolderTree
        spaces={[space("fld_a", "Team A"), space("fld_me", "Mine", { kind: "personal" })]}
        folder={folder}
        marking
        chain={chain}
        state={state}
        onChange={(n) => {
          changes += 1;
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
  changes = 0;
  // The reveal's hold and the failures already announced are kept in the user's stores: each test starts with none.
  resetUserStores();
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

  // Decision 88 (changes decision 69): a place deep in the tree opens the folders on the way to it, top down, and marks the place's
  // own row. Until the way is read, the nearest row on screen keeps the mark.
  it("opens the folders on the way to a place that is folded away, and marks the place's own row", async () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBe("location");
    expect(await screen.findByText("Deep")).toBeTruthy();
    expect(screen.getByRole("treeitem", { name: "Deep" }).getAttribute("aria-current")).toBe("location");
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBeNull();
    expect(latest.expanded).toEqual(["fld_a", "nod_one"]);
  });

  // Decision 69, review fix 8: the place changes after the tree is mounted: no folder on its way opens, and a row is marked only
  // when it is on screen.
  // Decision 88: a place changed after mount opens the way to it. A place whose folder is not in the tree (no space or top folder
  // is shown for it) opens nothing and marks nothing, so that rule of decision 69 is kept.
  it("a place changed after mount opens the way to it (decision 88); a place outside the tree opens nothing and marks nothing", async () => {
    const { rerender, unmount } = render(<Harness folder="fld_a" />);
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBe("location");
    rerender(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    expect(await screen.findByRole("treeitem", { name: "Deep" })).toBeTruthy();
    expect(latest.expanded).toEqual(["fld_a", "nod_one"]);
    unmount();
    // A fresh mount for a place outside the tree: nothing is read, opened or marked.
    render(<Harness folder="fld_deep" chain={["nod_one", "nod_deep"]} />);
    await new Promise((r) => setTimeout(r, 20));
    expect(document.querySelector('[aria-current="location"]')).toBeNull();
    expect(screen.queryByRole("treeitem", { name: "Deep" })).toBeNull();
    expect(screen.queryByText("Docs")).toBeNull();
    expect(latest.expanded).toEqual([]);
    expect(api.childrenPage).toHaveBeenCalledTimes(2);
  });

  // The start is folded: the reveal opens the branches above the place, and the place's own row is then the mark.
  it("marks the place's own row once the branches above it are read, and no row above it", async () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
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
    // The space's own root is not in the tree. The given folder is a row, and it is the mark until the way below it is read (decision 88:
    // the given folder is on the way, so it is opened and read; the place below it is not in its listing, so it stays the mark).
    expect(screen.queryByText("Team A")).toBeNull();
    const top = screen.getByText("Open").closest<HTMLElement>(".tree-row") as HTMLElement;
    expect(top.parentElement?.getAttribute("aria-current")).toBe("location");
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_a", "nod_top", undefined, { key: "name", dir: "asc" }));
    expect(api.childrenPage).not.toHaveBeenCalledWith("fld_a", null, expect.anything(), expect.anything());
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

// Decision 88: the sidebar's tree opens the way to the folder being viewed, top down, and marks that folder's own row. The tests use
// a folder three levels below the space's root, so the way has three folders on it (the space's root and two more).
describe("the folder tree opens the way to the place (decision 88)", () => {
  const readThreeDeep = async (folder: string, d: string | null) => {
    if (folder === "fld_a" && d === null) return { items: [dir("nod_one", "Docs"), dir("nod_two", "Pics", "viewer")], next_cursor: undefined };
    if (d === "nod_one") return { items: [dir("nod_deep", "Deep")], next_cursor: undefined };
    if (d === "nod_deep") return { items: [dir("nod_third", "Third")], next_cursor: undefined };
    return { items: [], next_cursor: undefined };
  };
  const threeDeep = () => api.childrenPage.mockImplementation(readThreeDeep);
  const deep = ["nod_one", "nod_deep", "nod_third"];
  const reads = () => api.childrenPage.mock.calls.map((c) => c[1]);

  it("opens the three folders on the way, each read once and top down, and marks the viewed folder's row", async () => {
    threeDeep();
    render(<Harness folder="fld_a" chain={deep} />);
    expect(await screen.findByRole("treeitem", { name: "Third" })).toBeTruthy();
    expect(screen.getByRole("treeitem", { name: "Third" }).getAttribute("aria-current")).toBe("location");
    expect(reads()).toEqual([null, "nod_one", "nod_deep"]);
    expect(latest.expanded).toEqual(["fld_a", "nod_one", "nod_deep"]);
  });

  it("opens nothing when the place is a space's own root, as landing on My files, and the next deep place opens its way", async () => {
    threeDeep();
    const { rerender } = render(<Harness folder="fld_me" chain={[]} />);
    expect(screen.getByRole("treeitem", { name: "My files" }).getAttribute("aria-current")).toBe("location");
    await new Promise((r) => setTimeout(r, 0));
    expect(api.childrenPage).not.toHaveBeenCalled();
    expect(latest.expanded).toEqual([]);
    rerender(<Harness folder="fld_a" chain={deep} />);
    expect(await screen.findByRole("treeitem", { name: "Third" })).toBeTruthy();
  });

  it("stops the way when the place changes while it is read: the old way is not opened, and the new place is marked", async () => {
    threeDeep();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((r) => (release = r));
    api.childrenPage.mockImplementation(async (folder: string, d: string | null) => {
      if (folder === "fld_a" && d === null) await gate;
      return readThreeDeep(folder, d);
    });
    const { rerender } = render(<Harness folder="fld_a" chain={deep} />);
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_a", null, undefined, expect.anything()));
    rerender(<Harness folder="fld_a" chain={["nod_two"]} />);
    release();
    expect(await screen.findByRole("treeitem", { name: "Pics" })).toBeTruthy();
    await waitFor(() => expect(screen.getByRole("treeitem", { name: "Pics" }).getAttribute("aria-current")).toBe("location"));
    expect(latest.expanded).toEqual(["fld_a"]);
    expect(reads()).not.toContain("nod_one");
    expect(reads()).not.toContain("nod_deep");
  });

  it("a hand close on the way moves the mark to that folder and stops the way; the next change of place reveals again", async () => {
    threeDeep();
    const { rerender } = render(<Harness folder="fld_a" chain={deep} />);
    expect(await screen.findByRole("treeitem", { name: "Third" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Collapse Docs" }));
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBe("location");
    expect(screen.queryByRole("treeitem", { name: "Third" })).toBeNull();
    await new Promise((r) => setTimeout(r, 0));
    expect(latest.expanded).not.toContain("nod_one");
    expect(screen.queryByRole("treeitem", { name: "Deep" })).toBeNull();
    // Another place, then the deep one again: the way is revealed again.
    rerender(<Harness folder="fld_a" chain={["nod_two"]} />);
    await waitFor(() => expect(screen.getByRole("treeitem", { name: "Pics" }).getAttribute("aria-current")).toBe("location"));
    rerender(<Harness folder="fld_a" chain={deep} />);
    expect(await screen.findByRole("treeitem", { name: "Third" })).toBeTruthy();
    expect(latest.expanded).toEqual(expect.arrayContaining(["fld_a", "nod_one", "nod_deep"]));
  });

  it("a folder on the way that fails to read leaves the mark on the deepest row that did read, and says only the failure", async () => {
    api.childrenPage.mockImplementation(async (folder: string, d: string | null) => {
      if (folder === "fld_a" && d === null) return { items: [dir("nod_one", "Docs")], next_cursor: undefined };
      throw new ApiError(500, "internal", {}, 0);
    });
    render(<Harness folder="fld_a" chain={deep} />);
    const alert = await screen.findByRole("alert", undefined, { timeout: 8000 });
    expect(within(alert).getByText(/could not be loaded/)).toBeTruthy();
    expect(screen.getByRole("treeitem", { name: "Team A" }).getAttribute("aria-current")).toBe("location");
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBeNull();
    expect(screen.queryByRole("treeitem", { name: "Third" })).toBeNull();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    // The root, then the folder that failed, which the client retries once: no other read.
    expect(reads()).toEqual([null, "nod_one", "nod_one"]);
  });

  // A way longer than the saved cap (60 folders) is opened and kept: every folder on it is open once the reveal settles, and the
  // reveal makes one change per folder, not a loop.
  it("opens a way of 70 folders, keeps all of them open, and the reveal makes one change per folder", async () => {
    const depth = 70;
    api.childrenPage.mockImplementation(async (folder: string, d: string | null) => {
      if (folder === "fld_a" && d === null) return { items: [dir("nod_1", "F1")], next_cursor: undefined };
      const i = Number(String(d).slice(4));
      return { items: i < depth ? [dir(`nod_${i + 1}`, `F${i + 1}`)] : [], next_cursor: undefined };
    });
    const chain = Array.from({ length: depth }, (_, i) => `nod_${i + 1}`);
    render(<Harness folder="fld_a" chain={chain} />);
    const way = ["fld_a", ...chain.slice(0, -1)];
    // The reads resolve at once (microtasks, no timers), so the wait is for the condition itself: every folder on the way is open and read,
    // and the viewed folder is marked. The timeout is generous and does not decide the result; a loop would never meet the condition.
    await waitFor(
      () => {
        expect(latest.expanded).toEqual(expect.arrayContaining(way));
        expect(reads()).toHaveLength(way.length);
        expect(screen.getByRole("treeitem", { name: `F${depth}` }).getAttribute("aria-current")).toBe("location");
      },
      { timeout: 30_000 },
    );
    expect(latest.expanded).toHaveLength(way.length);
    // One change per folder opened: a loop would send more changes than the way has folders.
    expect(changes).toBeLessThanOrEqual(way.length + 2);
  });

  // A hand close holds until the place changes, across a remount of the tree (the rail, the drawer, a breakpoint).
  it("a folder closed by hand on the way stays closed when the tree is mounted again for the same place", async () => {
    const first = render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    expect(await screen.findByText("Deep")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Collapse Docs" }));
    expect(screen.queryByText("Deep")).toBeNull();
    first.unmount();
    render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} start={latest} />);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("Deep")).toBeNull();
    expect(latest.expanded).not.toContain("nod_one");
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBe("location");
  });

  // A failed read on the way is announced once. A later change of place shows the same failure quietly, with its Try again.
  it("announces a failed read on the way once, and shows it without an alert on a later change of place", async () => {
    api.childrenPage.mockImplementation(async (folder: string, d: string | null) => {
      if (folder === "fld_a" && d === null) return { items: [dir("nod_one", "Docs"), dir("nod_two", "Pics", "viewer")], next_cursor: undefined };
      if (d === "nod_one") throw new ApiError(500, "internal", {}, 0);
      return { items: [], next_cursor: undefined };
    });
    const { rerender } = render(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    await screen.findByRole("alert", undefined, { timeout: 8000 });
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    rerender(<Harness folder="fld_a" chain={["nod_two"]} />);
    await waitFor(() => expect(screen.getByRole("treeitem", { name: "Pics" }).getAttribute("aria-current")).toBe("location"));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText(/could not be loaded/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
    rerender(<Harness folder="fld_a" chain={["nod_one", "nod_deep"]} />);
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  // A refetch that fails keeps the folder's children on screen, so the folder is still a row on the way and can take the mark.
  it("a folder whose refetch fails keeps its children, and its row stays the mark", async () => {
    render(<Harness folder="fld_a" chain={["nod_one", "nod_gone"]} />);
    expect(await screen.findByRole("treeitem", { name: "Docs" })).toBeTruthy();
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBe("location");
    api.childrenPage.mockRejectedValue(new ApiError(500, "internal", {}, 0));
    await client?.invalidateQueries({ queryKey: queryKey("tree", "fld_a", "nod_one") });
    await screen.findByRole("alert", undefined, { timeout: 8000 });
    expect(screen.getByText("Deep")).toBeTruthy();
    expect(screen.getByRole("treeitem", { name: "Docs" }).getAttribute("aria-current")).toBe("location");
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

// Decision 81: every row of the sidebar's tree takes the menu row's metrics (the same height, inline margin and radius as an
// antd menu item, read from the same tokens), on a mouse and on a finger. The tree outside the sidebar keeps its own rows.
describe("the folder tree in the sidebar: the menu row's metrics (decision 81)", () => {
  const rowOf = (name: string) => screen.getByText(name).closest<HTMLElement>(".tree-row");

  // The expected metrics are read from the same theme the menu reads (sidebarRow over antd's design token), not written as numbers,
  // so a change of the theme moves both sides and a tree that stops reading sidebarRow fails.
  it("gives every sidebar row the menu item's height, inline margin and radius on a mouse", async () => {
    const menu = sidebarRow(antdTheme.getDesignToken(), false);
    render(<Harness colours={SIDEBAR} folder="fld_a" start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    for (const name of ["My files", "Team A", "Docs"]) {
      const style = rowOf(name)?.getAttribute("style") ?? "";
      expect(style, name).toContain(`min-block-size: ${menu.height}px`);
      expect(style, name).toContain(`margin-inline: ${menu.inline}px`);
      expect(style, name).toContain(`border-radius: ${menu.radius}px`);
    }
  });

  it("gives every sidebar row the menu item's height on a finger", async () => {
    const menu = sidebarRow(antdTheme.getDesignToken(), true);
    render(<Harness colours={SIDEBAR} coarse folder="fld_a" start={{ expanded: ["fld_a"] }} />);
    await screen.findByText("Docs");
    for (const name of ["My files", "Team A", "Docs"]) {
      expect(rowOf(name)?.getAttribute("style") ?? "", name).toContain(`min-block-size: ${menu.height}px`);
    }
  });

  it("keeps a tree outside the sidebar at its own row size, with no inline margin", () => {
    render(<Harness start={{ expanded: ["fld_a"] }} />);
    const style = rowOf("My files")?.getAttribute("style") ?? "";
    expect(style).toContain("min-block-size: 32px");
    expect(style).not.toContain("margin-inline");
  });
});
