// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's Playlists tab: the person's saved playlists and the automatic Continue watching list, side by side on a
// desktop and stacked on a phone. The left table lists Continue watching first (its Automatic tag and first-page count),
// then Recently added, then the saved ones; the address's list= picks the detail. The detail plays its available rows through
// the page's one session, shows the place and time left for Continue watching, and edits a saved playlist (rename, delete,
// move, remove) only on the person's action. An unavailable entry is italic, tagged, and never played. The API is replaced at
// the fetch boundary. Recently added's own checks sit beside these, in MediaPlaylistsRecent.test.tsx.

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { folders } from "../../lib/files/folders";
import { getPlayback, stopPlayback } from "../../lib/files/playback";
import { setQueueControl } from "../../lib/files/queueControl";
import { resetTags } from "../../lib/files/tags";
import type { FolderAccess, Node } from "../../lib/files/types";
import { session, type Session } from "../../lib/session";
import { expectEmptyTable } from "../../test/emptyTable";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { desktopViewport, phoneViewport } from "../../test/viewport";
import MediaPlaylistsTab from "./MediaPlaylistsTab";

// The picker is a lazy chunk of its own; here its props are what the tab passes, and its pick is called by hand.
const pick = vi.hoisted(() => ({ props: null as Record<string, unknown> | null }));
vi.mock("../files/PickFiles", async () => {
  const { createElement } = await import("react");
  return {
    default: (p: Record<string, unknown>) => {
      pick.props = p;
      return createElement("div", { "data-testid": "pick-mock" });
    },
  };
});

// The destination picker is a lazy chunk too; its pick is called by hand.
const dest = vi.hoisted(() => ({ props: null as Record<string, unknown> | null }));
vi.mock("../files/PickDestination", async () => {
  const { createElement } = await import("react");
  return {
    default: (p: Record<string, unknown>) => {
      dest.props = p;
      return createElement("div", { "data-testid": "dest-mock" });
    },
  };
});

interface PickProps {
  title: string;
  action: string;
  spaces: FolderAccess[];
  startAt?: string;
  accept: (n: Node) => boolean;
  multiple?: boolean;
  max?: number;
  onPick: (nodes: Node[]) => void;
  onClose: () => void;
}

interface DestProps {
  mode: string;
  heading?: string;
  action?: string;
  spaces: FolderAccess[];
  startAt?: string;
  onPick: (d: { folder: string; dir: string | null; name?: string }) => void;
  onClose: () => void;
}

const PLAYLISTS = "/api/v1/playlists";
const PLAYED = "/api/v1/media/played";
const TAGS = "/api/v1/media/tags";
const FOLDERS = "/api/v1/folders";
const SEARCH = "/api/v1/search";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

function file(id: string, name: string, mime: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    folder_id: "fld_aaaa1111",
    parent_id: "nod_bbbb2222",
    name,
    kind: "file",
    mime_type: mime,
    size: 10,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-02T00:00:00Z",
    role: "owner",
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

const clip = (id: string, name: string, extra: Record<string, unknown> = {}) => file(id, name, "video/webm", extra);
const song = (id: string, name: string, extra: Record<string, unknown> = {}) => file(id, name, "audio/mpeg", extra);

const played = (n: ReturnType<typeof file>, position: number, duration: number, at = "2026-11-12T10:00:00Z") => ({
  ...n,
  position_seconds: position,
  duration_seconds: duration,
  played_at: at,
});

function summary(id: string, name: string, count: number, updated = "2026-11-10T09:00:00Z") {
  return { id, name, entry_count: count, created_at: "2026-11-01T00:00:00Z", updated_at: updated };
}

function detailOf(id: string, name: string, entries: { entry_id: string; node_id: string; unavailable: boolean; node?: ReturnType<typeof file> }[]) {
  return { id, name, entry_count: entries.length, created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-10T09:00:00Z", entries };
}

const entry = (node: ReturnType<typeof file>, i: number) => ({ entry_id: `ple_${node.id}`, position: i, node_id: node.id, unavailable: false, node });
const goneEntry = (nodeID: string, i: number) => ({ entry_id: `ple_${nodeID}`, position: i, node_id: nodeID, unavailable: true });

function LocationProbe() {
  const loc = useLocation();
  return (
    <>
      <span data-testid="pl-loc">{loc.search}</span>
      <span data-testid="pl-state">{JSON.stringify(loc.state ?? null)}</span>
    </>
  );
}

function renderTab(onPlay: () => void = () => {}, entry: string | { pathname: string; search?: string; state?: unknown } = "/media?tab=playlists") {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <App>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route
              path="/media"
              element={
                <>
                  <MediaPlaylistsTab onPlay={onPlay} />
                  <LocationProbe />
                </>
              }
            />
            <Route path="/files" element={<p>files page</p>} />
            <Route path="/files/:fld" element={<p>folder page</p>} />
            <Route path="/files/:fld/:dir" element={<p>dir page</p>} />
          </Routes>
        </MemoryRouter>
      </App>
    </QueryClientProvider>,
  );
}

const paramsOf = (c: Call) => new URLSearchParams(c.url.split("?")[1] ?? "");
const row = (name: string) => screen.getByRole("row", { name });

// answers keeps the playlists, the played list, the search, the tags and the folders empty but for what a check gives.
function answers(opts: { lists?: unknown[]; playedItems?: unknown[]; nextCursor?: string; recentItems?: unknown[]; details?: Record<string, unknown> } = {}) {
  return (c: Call) => {
    if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
    if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(PLAYED)) {
      const body: Record<string, unknown> = { items: opts.playedItems ?? [] };
      if (opts.nextCursor) body.next_cursor = opts.nextCursor;
      return { status: 200, body };
    }
    if (c.url.startsWith(SEARCH)) return { status: 200, body: { items: opts.recentItems ?? [] } };
    if (c.url.startsWith(PLAYLISTS)) {
      const m = /\/api\/v1\/playlists\/([^/?]+)/.exec(c.url);
      if (m && c.method === "GET") {
        const d = opts.details?.[m[1]!];
        return d ? { status: 200, body: d } : fail(404, "not_found");
      }
      if (c.method === "GET") return { status: 200, body: { items: opts.lists ?? [] } };
      return { status: 200, body: {} };
    }
    return fail(404, "not_found");
  };
}

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  resetTags();
  session.set(signedAs("olivia"));
});
afterEach(() => {
  stopPlayback();
  setQueueControl(null);
  session.set(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  undoView();
});

describe("the Playlists tab's list", () => {
  it("lists Continue watching first with its Automatic tag and count, then Recently added, then the saved playlists", async () => {
    const { calls } = stubFetch(
      answers({
        lists: [summary("pls_a", "Evening", 3), summary("pls_b", "Road trip", 12)],
        playedItems: [played(clip("nod_c1", "one.webm"), 6, 60), played(song("nod_c2", "two.mp3"), 10, 0), played(clip("nod_c3", "three.webm"), 1, 0)],
      }),
    );
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(region.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(4));
    const rows = Array.from(region.querySelectorAll('tbody tr[role="row"][aria-label]'));
    expect(rows[0]!.textContent).toContain("Continue watching");
    expect(rows[0]!.textContent).toContain("Automatic");
    expect(rows[0]!.textContent).toContain("3");
    expect(rows[1]!.textContent).toContain("Recently added");
    expect(rows[1]!.textContent).toContain("Automatic");
    expect(rows[2]!.textContent).toContain("Evening");
    expect(rows[2]!.textContent).toContain("3");
    expect(rows[3]!.textContent).toContain("Road trip");
    expect(rows[3]!.textContent).toContain("12");
    const p = paramsOf(calls.find((c) => c.url.startsWith(PLAYED))!);
    expect(p.get("unfinished")).toBe("true");
    expect(p.get("limit")).toBe("50");
  });

  it("shows 50+ for Continue watching when its first page has a next page", async () => {
    const items = Array.from({ length: 50 }, (_, i) => played(clip(`nod_p${i}`, `p${i}.webm`), 6, 60));
    stubFetch(answers({ playedItems: items, nextCursor: "c1" }));
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(within(region).getByRole("row", { name: /Continue watching/ }).textContent).toContain("50+"));
  });

  it("selects Continue watching by default and writes list= with replace when a row is chosen", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists");
    fireEvent.click(row("Evening"));
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=pls_a"));
  });

  it("list= opens that playlist, and an unknown id falls back to Continue watching", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    expect(await screen.findByRole("heading", { name: "Evening" })).toBeTruthy();
    expect(screen.getByText("Playlist")).toBeTruthy();
  });

  it("an unknown list id falls back to Continue watching", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_bogus");
    expect(await screen.findByRole("heading", { name: "Continue watching" })).toBeTruthy();
    expect(screen.getByText("Automatic playlist")).toBeTruthy();
  });
});

describe("the Playlists tab's detail", () => {
  it("shows the columns, the place tag and the time left for Continue watching", async () => {
    stubFetch(
      answers({
        playedItems: [played(song("nod_c1", "song.mp3", { tags: { duration_ms: 2863000 } }), 741, 2863)],
      }),
    );
    renderTab();
    const detail = await screen.findByRole("region", { name: "Continue watching" });
    await waitFor(() => {
      const t = detail.querySelector("table") as HTMLElement;
      expect(within(t).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Title", "#", "Folder", "Length", "Actions"]);
    });
    const r = within(detail).getByRole("row", { name: "song.mp3" });
    expect(r.textContent).toContain("At 12:21");
    expect(r.textContent).toContain("35:22 left");
    expect(r.textContent).toContain("47:43");
  });

  it("marks an unavailable entry in italics with its tag and never plays it", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), goneEntry("nod_gone", 1)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 2)], details: { pls_a: d } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    const detail = await screen.findByRole("region", { name: "Evening" });
    const off = await within(detail).findByText("Deleted, or you no longer have access");
    expect(off.closest(".media-title--off")).not.toBeNull();
    const offRow = off.closest('[role="row"]') as HTMLElement;
    fireEvent.click(offRow);
    expect(getPlayback().seed).toBeNull();
  });

  it("Play starts the session at the first available row with every available row, and turns to Now playing", async () => {
    const d = detailOf("pls_a", "Evening", [goneEntry("nod_gone", 0), entry(clip("nod_a1", "a.webm"), 1), entry(clip("nod_a2", "b.webm"), 2)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 3)], details: { pls_a: d } }));
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_a1");
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_a1", "nod_a2"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("Shuffle and play starts the same session with shuffle on", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(clip("nod_a2", "b.webm"), 1)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 2)], details: { pls_a: d } }));
    // The random pick lands on the first row, so the start is the first available one.
    vi.spyOn(Math, "random").mockReturnValue(0);
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Shuffle and play" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_a1");
    expect(seed?.shuffle).toBe(true);
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_a1", "nod_a2"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("Shuffle and play starts at a random available row, not always the first", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(clip("nod_a2", "b.webm"), 1), entry(clip("nod_a3", "c.webm"), 2)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 3)], details: { pls_a: d } }));
    // A pick near the top of the range lands on the last available row.
    vi.spyOn(Math, "random").mockReturnValue(0.99);
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Shuffle and play" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_a3");
    expect(seed?.shuffle).toBe(true);
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_a1", "nod_a2", "nod_a3"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("a click on a row plays from it with the available rows as the selection", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(clip("nod_a2", "b.webm"), 1)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 2)], details: { pls_a: d } }));
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=pls_a");
    const detail = await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(within(detail).getByRole("row", { name: "b.webm" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_a2");
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_a1", "nod_a2"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("a Continue watching row plays from it too", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60), played(clip("nod_c2", "two.webm"), 8, 60)] }));
    const onPlay = vi.fn();
    renderTab(onPlay);
    const detail = await screen.findByRole("region", { name: "Continue watching" });
    fireEvent.click(within(detail).getByRole("row", { name: "two.webm" }));
    expect(getPlayback().seed?.start.id).toBe("nod_c2");
    expect(onPlay).toHaveBeenCalledTimes(1);
  });
});

describe("the Playlists tab's row menu", () => {
  const two = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(clip("nod_a2", "b.webm"), 1), entry(clip("nod_a3", "c.webm"), 2)]);

  it("a saved row offers Play from here, Open in its folder, Move up, Move down and Remove", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 3)], details: { pls_a: two } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    const detail = await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for b.webm" }));
    expect(await screen.findByRole("menuitem", { name: "Play from here" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Open in its folder" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: /Move up/ })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: /Move down/ })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: /Remove/ })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
  });

  it("a Continue watching row offers Play from here and Open in its folder, but no Move or Remove", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    const detail = await screen.findByRole("region", { name: "Continue watching" });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for one.webm" }));
    expect(await screen.findByRole("menuitem", { name: "Play from here" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Open in its folder" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /Move up/ })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /Remove/ })).toBeNull();
  });

  it("Move up sends the new order and Remove sends the entry", async () => {
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 3)], details: { pls_a: two } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    const detail = await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for b.webm" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Move up/ }));
    await waitFor(() => {
      const put = calls.find((c) => c.method === "PUT" && c.url.endsWith("/order"));
      expect(put).toBeTruthy();
      expect((put!.body as { entries: string[] }).entries).toEqual(["ple_nod_a2", "ple_nod_a1", "ple_nod_a3"]);
    });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for b.webm" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Remove/ }));
    await waitFor(() => {
      const del = calls.find((c) => c.method === "DELETE" && c.url.includes("/entries/ple_nod_a2"));
      expect(del).toBeTruthy();
    });
  });

  it("Play next shows only while a session plays, and adds the file after the current one", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 3)], details: { pls_a: two } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    const detail = await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for b.webm" }));
    await screen.findByRole("menuitem", { name: "Play from here" });
    expect(screen.queryByRole("menuitem", { name: "Play next" })).toBeNull();
    fireEvent.click(screen.getByRole("menuitem", { name: "Play from here" }));
    await waitFor(() => expect(getPlayback().seed).not.toBeNull());

    const addNext = vi.fn();
    setQueueControl({ addNext });
    fireEvent.click(within(detail).getByRole("button", { name: "More actions for c.webm" }));
    const item = await screen.findByRole("menuitem", { name: "Play next" });
    fireEvent.click(item);
    expect(addNext).toHaveBeenCalledTimes(1);
    expect(addNext.mock.calls[0]![0].id).toBe("nod_a3");
  });
});

describe("the Playlists tab's rename and delete", () => {
  it("Rename changes the name through the modal and refreshes the list", async () => {
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0)]) } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "Late night" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      const patch = calls.find((c) => c.method === "PATCH" && c.url.endsWith("/playlists/pls_a"));
      expect(patch).toBeTruthy();
      expect((patch!.body as { name: string }).name).toBe("Late night");
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url === PLAYLISTS).length).toBeGreaterThanOrEqual(2));
  });

  it("Delete asks to confirm, then deletes and refreshes the list", async () => {
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0)]) } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const confirm = await screen.findByText("Delete this playlist? The files stay");
    expect(confirm).toBeTruthy();
    const pop = confirm.closest(".ant-popconfirm") as HTMLElement;
    fireEvent.click(within(pop).getByRole("button", { name: "Delete" }));
    await waitFor(() => {
      const del = calls.find((c) => c.method === "DELETE" && c.url.endsWith("/playlists/pls_a"));
      expect(del).toBeTruthy();
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url === PLAYLISTS).length).toBeGreaterThanOrEqual(2));
  });
});

describe("the Playlists tab's empty states", () => {
  it("with no saved playlists the list keeps only the two automatic rows", async () => {
    stubFetch(answers({ lists: [], playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(region.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(2));
    expect(within(region).getByRole("row", { name: /Continue watching/ })).toBeTruthy();
    expect(within(region).getByRole("row", { name: /Recently added/ })).toBeTruthy();
    expect(within(region).queryByText("No saved playlists yet. Save the queue to keep it.")).toBeNull();
  });

  it("Continue watching empty keeps the table's frame and offers Open Videos", async () => {
    stubFetch(answers({ lists: [], playedItems: [] }));
    renderTab();
    const action = await expectEmptyTable("Continue watching", {
      headers: ["Title", "#", "Folder", "Length"],
      title: "Nothing to continue",
      description: "Videos and songs you stop part way show here",
      action: "Open Videos",
    });
    fireEvent.click(action!);
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=videos"));
  });

  it("a saved playlist with no rows keeps the table's frame and offers Open My files", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 0)], details: { pls_a: detailOf("pls_a", "Evening", []) } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    const action = await expectEmptyTable("Evening", {
      headers: ["Title", "#", "Folder", "Length"],
      title: "This playlist is empty",
      description: "Play files on Now playing, then save the queue as a playlist",
      action: "Open My files",
    });
    fireEvent.click(action!);
    expect(await screen.findByText("files page")).toBeTruthy();
  });
});

describe("the Playlists tab's failures and layout", () => {
  it("a failed first load shows the error with a retry that reads again", async () => {
    let n = 0;
    stubFetch((c) => {
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      if (c.url.startsWith(PLAYED)) return { status: 200, body: { items: [] } };
      if (c.url === PLAYLISTS && c.method === "GET") {
        n++;
        return n === 1 ? fail(500, "internal") : { status: 200, body: { items: [summary("pls_a", "Evening", 1)] } };
      }
      return fail(404, "not_found");
    });
    renderTab();
    const alert = await screen.findByRole("alert");
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("row", { name: /Evening/ })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("stacks the list above the detail at phone width", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    const undo = phoneViewport();
    try {
      renderTab();
      const layout = await screen.findByTestId("media-pl-layout");
      await waitFor(() => expect(layout.className).toContain("media-pl-layout--phone"));
      const list = screen.getByRole("region", { name: "Your playlists" });
      const detail = screen.getByRole("region", { name: "Continue watching" });
      expect(list.compareDocumentPosition(detail) & globalThis.Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    } finally {
      undo();
    }
  });

  it("sits the list beside the detail on a desktop", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    const layout = await screen.findByTestId("media-pl-layout");
    await waitFor(() => expect(layout.className).not.toContain("media-pl-layout--phone"));
  });
});

describe("the Playlists tab's New playlist", () => {
  it("the name dialog keeps the queue save's name rules", async () => {
    stubFetch(answers({ lists: [] }));
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "New playlist" }));
    const input = (await screen.findByLabelText("Name")) as HTMLInputElement;
    expect(input.maxLength).toBe(120);
    const create = screen.getByRole("button", { name: "Create" }) as HTMLButtonElement;
    expect(create.disabled).toBe(true);
    fireEvent.change(input, { target: { value: "   " } });
    expect(create.disabled).toBe(true);
    fireEvent.change(input, { target: { value: "Road trip" } });
    expect(create.disabled).toBe(false);
    fireEvent.change(input, { target: { value: "x".repeat(121) } });
    expect(create.disabled).toBe(true);
    fireEvent.change(input, { target: { value: "x".repeat(120) } });
    expect(create.disabled).toBe(false);
  });

  it("Create makes the playlist with no files, once, and opens it", async () => {
    let created = false;
    const { calls } = stubFetch((c) => {
      if (c.url === PLAYLISTS && c.method === "POST") {
        created = true;
        return { status: 201, body: summary("pls_new", "Road trip", 0) };
      }
      if (c.url === PLAYLISTS && c.method === "GET") {
        return { status: 200, body: { items: created ? [summary("pls_new", "Road trip", 0)] : [] } };
      }
      return answers({ details: { pls_new: detailOf("pls_new", "Road trip", []) } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "New playlist" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "  Road trip  " } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 });
    await waitFor(() => {
      const posts = calls.filter((c) => c.method === "POST" && c.url === PLAYLISTS);
      expect(posts.length).toBe(1);
      expect(posts[0]!.body).toEqual({ name: "Road trip", nodes: [] });
    });
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=pls_new"));
    expect(await screen.findByRole("heading", { name: "Road trip" })).toBeTruthy();
    expect(await screen.findByText("This playlist is empty")).toBeTruthy();
  });

  it("a second Enter while the create runs makes no second playlist, and the button waits", async () => {
    let gate!: () => void;
    const held = new Promise<void>((res) => (gate = res));
    const { calls } = stubFetch(async (c) => {
      if (c.url === PLAYLISTS && c.method === "POST") {
        await held;
        return { status: 201, body: summary("pls_new", "Road trip", 0) };
      }
      if (c.url === PLAYLISTS && c.method === "GET") {
        return { status: 200, body: { items: [summary("pls_new", "Road trip", 0)] } };
      }
      return answers({ details: { pls_new: detailOf("pls_new", "Road trip", []) } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "New playlist" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "Road trip" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST" && c.url === PLAYLISTS).length).toBe(1));
    expect((screen.getByRole("button", { name: "New playlist" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      gate();
    });
    await waitFor(() => expect((screen.getByRole("button", { name: "New playlist" }) as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "POST" && c.url === PLAYLISTS).length).toBe(1);
  });

  it("a 409 shows the tab's error and the dialog stays for a retry", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url === PLAYLISTS && c.method === "POST") return fail(409, "conflict", { reason: "limit", limit: 100 });
      return answers({ lists: [] })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "New playlist" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "Road trip" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("That did not work. Try again.")).toBeTruthy();
    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists");
    expect(calls.filter((c) => c.method === "POST" && c.url === PLAYLISTS).length).toBe(1);
  });
});

describe("the Playlists tab's Add files", () => {
  const personal: FolderAccess = {
    id: "fld_mine",
    name: "My space",
    kind: "personal",
    access: "write",
    role: "manager",
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
  };
  beforeEach(() => {
    folders.set([personal]);
  });

  it("opens the picker on the personal space, with the media accept and the room left", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(song("nod_a2", "b.mp3"), 1)]);
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 2)], details: { pls_a: d } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    expect(p.title).toBe("Add files to Evening");
    expect(p.action).toBe("Add");
    expect(p.multiple).toBe(true);
    expect(p.max).toBe(498);
    expect(p.startAt).toBe("fld_mine");
    expect(p.spaces.map((s) => s.id)).toContain("fld_mine");
    expect(p.accept(song("nod_x", "x.mp3") as unknown as Node)).toBe(true);
    expect(p.accept(clip("nod_y", "y.webm") as unknown as Node)).toBe(true);
    expect(p.accept(file("nod_z", "z.txt", "text/plain") as unknown as Node)).toBe(false);
    expect(p.accept({ ...file("nod_d", "d", "text/plain"), kind: "dir" } as unknown as Node)).toBe(false);
    expect(p.accept({ ...file("nod_s", "s", "audio/mpeg"), kind: "shortcut" } as unknown as Node)).toBe(false);
  });

  it("Continue watching has no Add files", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    await screen.findByRole("region", { name: "Continue watching" });
    expect(screen.queryByRole("button", { name: "Add files" })).toBeNull();
  });

  it("the pick sends one replaceEntries, old IDs then new, skips the ones already there, and re-reads", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0), entry(song("nod_a2", "b.mp3"), 1)]);
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 2)], details: { pls_a: d } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    const before = calls.filter((c) => c.method === "GET" && c.url === `${PLAYLISTS}/pls_a`).length;
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    act(() => {
      p.onPick([song("nod_a2", "b.mp3") as unknown as Node, clip("nod_b1", "c.webm") as unknown as Node, song("nod_b2", "d.mp3") as unknown as Node]);
    });
    await waitFor(() => {
      const puts = calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`);
      expect(puts.length).toBe(1);
      expect(puts[0]!.body).toEqual({ nodes: ["nod_a1", "nod_a2", "nod_b1", "nod_b2"] });
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url === `${PLAYLISTS}/pls_a`).length).toBe(before + 1));
    expect(screen.queryByTestId("pick-mock")).toBeNull();
  });

  it("the 500 cap disables Add files and its tooltip says why", async () => {
    const entries = Array.from({ length: 500 }, (_, i) => goneEntry(`nod_g${i}`, i));
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 500)], details: { pls_a: detailOf("pls_a", "Evening", entries) } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    await waitFor(() => expect(document.querySelector(".media-pl-summary")?.textContent?.startsWith("500")).toBe(true));
    const add = screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    fireEvent.pointerEnter(add.parentElement as HTMLElement, { pointerType: "mouse" });
    await waitFor(() => {
      const tip = document.querySelector('[role="tooltip"]');
      expect(tip?.textContent).toBe("A playlist holds at most 500 files");
    });
  });

  it("a second Add waits for the first replace and its re-read, so the first batch is not lost", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0)]);
    let gate!: () => void;
    const held = new Promise<void>((res) => (gate = res));
    const { calls } = stubFetch(async (c) => {
      if (c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`) {
        await held;
        return { status: 200, body: {} };
      }
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    const before = calls.filter((c) => c.method === "GET" && c.url === `${PLAYLISTS}/pls_a`).length;
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    act(() => {
      p.onPick([song("nod_b1", "c.mp3") as unknown as Node]);
    });
    // The first replace is on its way and held: Add files waits, off.
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`).length).toBe(1));
    expect((screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement).disabled).toBe(true);
    // A second pick while the first runs sends no second replace.
    act(() => {
      p.onPick([song("nod_b2", "d.mp3") as unknown as Node]);
    });
    expect(calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`).length).toBe(1);
    expect((screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement).disabled).toBe(true);
    // The replace lands and the detail re-reads; only then is Add files on again.
    await act(async () => {
      gate();
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url === `${PLAYLISTS}/pls_a`).length).toBe(before + 1));
    await waitFor(() => expect((screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`).length).toBe(1);
  });

  it("a failed replace re-enables Add files and shows the tab's error", async () => {
    const d = detailOf("pls_a", "Evening", [entry(clip("nod_a1", "a.webm"), 0)]);
    let gate!: () => void;
    const held = new Promise<void>((res) => (gate = res));
    const { calls } = stubFetch(async (c) => {
      if (c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`) {
        await held;
        return fail(500, "internal");
      }
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    act(() => {
      p.onPick([song("nod_b1", "c.mp3") as unknown as Node]);
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`).length).toBe(1));
    expect((screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      gate();
    });
    expect(await screen.findByText("That did not work. Try again.")).toBeTruthy();
    await waitFor(() => expect((screen.getByRole("button", { name: "Add files" }) as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "PUT" && c.url === `${PLAYLISTS}/pls_a/entries`).length).toBe(1);
  });
});

// Import reads an .m3u or .m3u8 file into a new playlist (decision 237). The skipped lines are told in a note over the
// detail, and the Files page hands the same note over in the router state.
describe("the Playlists tab's Import", () => {
  const personal: FolderAccess = {
    id: "fld_mine",
    name: "My space",
    kind: "personal",
    access: "write",
    role: "manager",
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
  };
  const m3u = (id: string, name: string) => file(id, name, "audio/x-mpegurl");
  const imported = detailOf("pls_imp", "Mix", [entry(song("nod_i1", "one.mp3"), 0)]);
  const importAnswer = { playlist: imported, skipped: [{ line: 2, reason: "url" }, { line: 5, reason: "not_found" }], skipped_total: 4 };
  beforeEach(() => {
    folders.set([personal]);
  });

  it("opens the picker on the personal space, for one .m3u or .m3u8 file", async () => {
    stubFetch(answers({}));
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    expect(p.title).toBe("Import a playlist file");
    expect(p.action).toBe("Import");
    expect(p.multiple).toBe(false);
    expect(p.startAt).toBe("fld_mine");
    expect(p.spaces.map((s) => s.id)).toContain("fld_mine");
    expect(p.accept(m3u("nod_m", "mix.m3u8") as unknown as Node)).toBe(true);
    expect(p.accept(m3u("nod_m2", "mix.M3U") as unknown as Node)).toBe(true);
    expect(p.accept(song("nod_x", "x.mp3") as unknown as Node)).toBe(false);
    expect(p.accept({ ...file("nod_d", "d", "text/plain"), kind: "dir" } as unknown as Node)).toBe(false);
  });

  it("the pick imports the file, selects the new playlist, and tells the skipped lines", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/import`) return { status: 201, body: importAnswer };
      return answers({ lists: [summary("pls_imp", "Mix", 1)], details: { pls_imp: imported } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    act(() => {
      (pick.props as unknown as PickProps).onPick([m3u("nod_f", "mix.m3u8") as unknown as Node]);
    });
    await waitFor(() => {
      const posts = calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/import`);
      expect(posts.length).toBe(1);
      expect(posts[0]!.body).toEqual({ node_id: "nod_f" });
    });
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=pls_imp"));
    expect(await screen.findByRole("region", { name: "Mix" })).toBeTruthy();
    expect(await screen.findByText("2 lines were skipped")).toBeTruthy();
    expect(screen.queryByTestId("pick-mock")).toBeNull();
  });

  it("the details name each skipped line and say how many more there are", async () => {
    stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/import`) return { status: 201, body: importAnswer };
      return answers({ lists: [summary("pls_imp", "Mix", 1)], details: { pls_imp: imported } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    act(() => {
      (pick.props as unknown as PickProps).onPick([m3u("nod_f", "mix.m3u8") as unknown as Node]);
    });
    fireEvent.click(await screen.findByRole("button", { name: "Show details" }));
    expect(await screen.findByText("Line 2: a web address")).toBeTruthy();
    expect(screen.getByText("Line 5: not found, or you cannot open it")).toBeTruthy();
    expect(screen.getByText("and 2 more")).toBeTruthy();
  });

  it("the note goes when another playlist is picked", async () => {
    const other = detailOf("pls_b", "Road trip", []);
    stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/import`) return { status: 201, body: importAnswer };
      return answers({ lists: [summary("pls_imp", "Mix", 1), summary("pls_b", "Road trip", 0)], details: { pls_imp: imported, pls_b: other } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    act(() => {
      (pick.props as unknown as PickProps).onPick([m3u("nod_f", "mix.m3u8") as unknown as Node]);
    });
    expect(await screen.findByText("2 lines were skipped")).toBeTruthy();
    fireEvent.click(row("Road trip"));
    await screen.findByRole("region", { name: "Road trip" });
    expect(screen.queryByText("2 lines were skipped")).toBeNull();
  });

  it("a second import waits for the first, so one file makes one playlist", async () => {
    let gate!: () => void;
    const held = new Promise<void>((res) => (gate = res));
    const { calls } = stubFetch(async (c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/import`) {
        await held;
        return { status: 201, body: importAnswer };
      }
      return answers({ lists: [summary("pls_imp", "Mix", 1)], details: { pls_imp: imported } })(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    const p = pick.props as unknown as PickProps;
    act(() => {
      p.onPick([m3u("nod_f", "mix.m3u8") as unknown as Node]);
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/import`).length).toBe(1));
    expect((screen.getByRole("button", { name: "Import a playlist file" }) as HTMLButtonElement).disabled).toBe(true);
    act(() => {
      p.onPick([m3u("nod_g", "other.m3u") as unknown as Node]);
    });
    expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/import`).length).toBe(1);
    await act(async () => {
      gate();
    });
    await waitFor(() => expect((screen.getByRole("button", { name: "Import a playlist file" }) as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/import`).length).toBe(1);
  });

  it("an import refusal shows its own sentence", async () => {
    stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/import`) return fail(409, "conflict", { reason: "limit" });
      return answers({})(c);
    });
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(screen.getByRole("button", { name: "Import a playlist file" }));
    await screen.findByTestId("pick-mock");
    act(() => {
      (pick.props as unknown as PickProps).onPick([m3u("nod_f", "mix.m3u8") as unknown as Node]);
    });
    expect(await screen.findByText("You have 100 playlists, the most allowed. Delete one before importing another.")).toBeTruthy();
    await waitFor(() => expect((screen.getByRole("button", { name: "Import a playlist file" }) as HTMLButtonElement).disabled).toBe(false));
  });
});

describe("the import handoff from the Files page", () => {
  const imported = detailOf("pls_imp", "Mix", [entry(song("nod_i1", "one.mp3"), 0)]);

  it("reads the skipped lines from the router state once, for the playlist the address names, and clears the state", async () => {
    stubFetch(answers({ lists: [summary("pls_imp", "Mix", 1)], details: { pls_imp: imported } }));
    renderTab(() => {}, {
      pathname: "/media",
      search: "?tab=playlists&list=pls_imp",
      state: { playlistImport: { skipped: [{ line: 3, reason: "outside" }], skipped_total: 1 } },
    });
    expect(await screen.findByText("1 line was skipped")).toBeTruthy();
    expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=pls_imp");
    expect(screen.getByTestId("pl-state").textContent).toBe("null");
  });

  it("keeps no note without the state, and none on Continue watching", async () => {
    stubFetch(answers({ lists: [summary("pls_imp", "Mix", 1)], details: { pls_imp: imported }, playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    const first = renderTab(() => {}, "/media?tab=playlists&list=pls_imp");
    await screen.findByRole("region", { name: "Mix" });
    expect(screen.queryByText("1 line was skipped")).toBeNull();
    first.unmount();
    renderTab(() => {}, { pathname: "/media", search: "?tab=playlists", state: { playlistImport: { skipped: [{ line: 3, reason: "outside" }], skipped_total: 1 } } });
    await screen.findByRole("region", { name: "Continue watching" });
    expect(screen.queryByText("1 line was skipped")).toBeNull();
  });
});

// Export writes a saved playlist as an .m3u8 file into a folder the person picks (decision 237).
describe("the Playlists tab's Export", () => {
  const personal: FolderAccess = {
    id: "fld_mine",
    name: "My space",
    kind: "personal",
    access: "write",
    role: "manager",
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
    root_node_id: "nod_root",
  };
  const d = detailOf("pls_a", "Evening", [entry(song("nod_a1", "a.mp3"), 0)]);
  const exportAnswer = { node: { id: "nod_out", name: "Evening (2).m3u8" }, left_out: 0 };
  beforeEach(() => {
    folders.set([personal]);
  });

  it("opens the save picker on the personal space", async () => {
    stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } }));
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    const p = dest.props as unknown as DestProps;
    expect(p.mode).toBe("save");
    expect(p.heading).toBe("Export playlist");
    expect(p.action).toBe("Export here");
    expect(p.startAt).toBe("fld_mine");
    expect(p.spaces.map((s) => s.id)).toContain("fld_mine");
  });

  it("the pick exports into the chosen folder and offers the new file's place", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`) return { status: 201, body: exportAnswer };
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    act(() => {
      (dest.props as unknown as DestProps).onPick({ folder: "fld_mine", dir: "nod_dir1", name: "Music" });
    });
    await waitFor(() => {
      const posts = calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`);
      expect(posts.length).toBe(1);
      expect(posts[0]!.body).toEqual({ folder_id: "nod_dir1" });
    });
    expect(await screen.findByText(/Saved as Evening \(2\)\.m3u8 in Music/)).toBeTruthy();
    expect(screen.queryByTestId("dest-mock")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open folder" }));
    expect(await screen.findByText("dir page")).toBeTruthy();
  });

  it("a pick of the space's root exports into its root node", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`) return { status: 201, body: exportAnswer };
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    act(() => {
      (dest.props as unknown as DestProps).onPick({ folder: "fld_mine", dir: null, name: "My space" });
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`)[0]!.body).toEqual({ folder_id: "nod_root" }));
    expect(await screen.findByText(/Saved as Evening \(2\)\.m3u8 in My space/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Open folder" }));
    expect(await screen.findByText("folder page")).toBeTruthy();
  });

  it("the left-out count joins the message", async () => {
    stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`) return { status: 201, body: { node: { id: "nod_out", name: "Evening.m3u8" }, left_out: 2 } };
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    act(() => {
      (dest.props as unknown as DestProps).onPick({ folder: "fld_mine", dir: "nod_dir1", name: "Music" });
    });
    expect(await screen.findByText(/2 entries were left out: files you cannot reach now or in another space/)).toBeTruthy();
  });

  it("Continue watching and Recently added have no Export", async () => {
    stubFetch(answers({ playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)], recentItems: [song("nod_r1", "new.mp3")] }));
    renderTab();
    await screen.findByRole("region", { name: "Continue watching" });
    await screen.findByRole("region", { name: "Recently added" });
    expect(screen.queryByRole("button", { name: "Export to a folder" })).toBeNull();
  });

  it("a second export waits for the first", async () => {
    let gate!: () => void;
    const held = new Promise<void>((res) => (gate = res));
    const { calls } = stubFetch(async (c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`) {
        await held;
        return { status: 201, body: exportAnswer };
      }
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    const p = dest.props as unknown as DestProps;
    act(() => {
      p.onPick({ folder: "fld_mine", dir: "nod_dir1", name: "Music" });
    });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`).length).toBe(1));
    expect((screen.getByRole("button", { name: "Export to a folder" }) as HTMLButtonElement).disabled).toBe(true);
    act(() => {
      p.onPick({ folder: "fld_mine", dir: "nod_dir2", name: "Other" });
    });
    expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`).length).toBe(1);
    await act(async () => {
      gate();
    });
    await waitFor(() => expect((screen.getByRole("button", { name: "Export to a folder" }) as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`).length).toBe(1);
  });

  it("an export refusal shows its own sentence", async () => {
    stubFetch((c) => {
      if (c.method === "POST" && c.url === `${PLAYLISTS}/pls_a/export`) return fail(409, "conflict", { reason: "moved" });
      return answers({ lists: [summary("pls_a", "Evening", 1)], details: { pls_a: d } })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=pls_a");
    await screen.findByRole("region", { name: "Evening" });
    fireEvent.click(screen.getByRole("button", { name: "Export to a folder" }));
    await screen.findByTestId("dest-mock");
    act(() => {
      (dest.props as unknown as DestProps).onPick({ folder: "fld_mine", dir: "nod_dir1", name: "Music" });
    });
    expect(await screen.findByText("The folder moved while exporting. Try again.")).toBeTruthy();
    await waitFor(() => expect((screen.getByRole("button", { name: "Export to a folder" }) as HTMLButtonElement).disabled).toBe(false));
  });
});