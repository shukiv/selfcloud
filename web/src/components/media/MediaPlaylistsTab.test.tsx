// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's Playlists tab: the person's saved playlists and the automatic Continue watching list, side by side on a
// desktop and stacked on a phone. The left table lists Continue watching first (its Automatic tag and first-page count) then
// the saved ones; the address's list= picks the detail. The detail plays its available rows through the page's one session,
// shows the place and time left for Continue watching, and edits a saved playlist (rename, delete, move, remove) only on the
// person's action. An unavailable entry is italic, tagged, and never played. The API is replaced at the fetch boundary.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { getPlayback, stopPlayback } from "../../lib/files/playback";
import { setQueueControl } from "../../lib/files/queueControl";
import { resetTags } from "../../lib/files/tags";
import { session, type Session } from "../../lib/session";
import { expectEmptyTable } from "../../test/emptyTable";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { desktopViewport, phoneViewport } from "../../test/viewport";
import MediaPlaylistsTab from "./MediaPlaylistsTab";

const PLAYLISTS = "/api/v1/playlists";
const PLAYED = "/api/v1/media/played";
const TAGS = "/api/v1/media/tags";
const FOLDERS = "/api/v1/folders";

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
  return <span data-testid="pl-loc">{loc.search}</span>;
}

function renderTab(onPlay: () => void = () => {}, entry = "/media?tab=playlists") {
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

// answers keeps the playlists, the played list, the tags and the folders empty but for what a check gives.
function answers(opts: { lists?: unknown[]; playedItems?: unknown[]; nextCursor?: string; details?: Record<string, unknown> } = {}) {
  return (c: Call) => {
    if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
    if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(PLAYED)) {
      const body: Record<string, unknown> = { items: opts.playedItems ?? [] };
      if (opts.nextCursor) body.next_cursor = opts.nextCursor;
      return { status: 200, body };
    }
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
  undoView();
});

describe("the Playlists tab's list", () => {
  it("lists Continue watching first with its Automatic tag and count, then the saved playlists", async () => {
    const { calls } = stubFetch(
      answers({
        lists: [summary("pls_a", "Evening", 3), summary("pls_b", "Road trip", 12)],
        playedItems: [played(clip("nod_c1", "one.webm"), 6, 60), played(song("nod_c2", "two.mp3"), 10, 0), played(clip("nod_c3", "three.webm"), 1, 0)],
      }),
    );
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(region.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(3));
    const rows = Array.from(region.querySelectorAll('tbody tr[role="row"][aria-label]'));
    expect(rows[0]!.textContent).toContain("Continue watching");
    expect(rows[0]!.textContent).toContain("Automatic");
    expect(rows[0]!.textContent).toContain("3");
    expect(rows[1]!.textContent).toContain("Evening");
    expect(rows[1]!.textContent).toContain("3");
    expect(rows[2]!.textContent).toContain("Road trip");
    expect(rows[2]!.textContent).toContain("12");
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
  it("with no saved playlists the list keeps only the Continue watching row", async () => {
    stubFetch(answers({ lists: [], playedItems: [played(clip("nod_c1", "one.webm"), 6, 60)] }));
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(region.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(1));
    expect(within(region).getByRole("row", { name: /Continue watching/ })).toBeTruthy();
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