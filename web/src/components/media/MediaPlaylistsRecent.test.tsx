// SPDX-License-Identifier: AGPL-3.0-or-later

// The Playlists tab's second automatic list, Recently added: the person's video and audio files created in the last 30 days,
// newest first, read through the search. The left table lists it right after Continue watching; the address's list=recent
// picks its detail, which plays its rows and shows the added date instead of the length, with no Add files, Rename or Delete.
// The API is replaced at the fetch boundary, and the clock is fixed so the search's created_after is known.

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
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { desktopViewport } from "../../test/viewport";
import MediaPlaylistsTab from "./MediaPlaylistsTab";

const PLAYLISTS = "/api/v1/playlists";
const PLAYED = "/api/v1/media/played";
const TAGS = "/api/v1/media/tags";
const FOLDERS = "/api/v1/folders";
const SEARCH = "/api/v1/search";

// The fixed clock: 2026-11-15T12:00:00Z. Recently added asks for the files created after the same moment 30 days earlier.
const CLOCK = Date.UTC(2026, 10, 15, 12, 0, 0);
const AFTER = CLOCK - 30 * 24 * 60 * 60 * 1000;
const RECENT_QUERY = `type=video&type=audio&kind=file&order=newest&limit=50&created_after=${AFTER}`;

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
    created_at: "2026-11-14T10:00:00Z",
    modified_at: "2026-11-14T10:00:00Z",
    role: "owner",
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

const clip = (id: string, name: string, extra: Record<string, unknown> = {}) => file(id, name, "video/webm", extra);
const song = (id: string, name: string, extra: Record<string, unknown> = {}) => file(id, name, "audio/mpeg", extra);

function summary(id: string, name: string, count: number) {
  return { id, name, entry_count: count, created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-10T09:00:00Z" };
}

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

const row = (name: string) => screen.getByRole("row", { name });

// answers keeps the playlists, the played list, the search, the tags and the folders empty but for what a check gives.
function answers(opts: { lists?: unknown[]; recentItems?: unknown[]; recentCursor?: string } = {}) {
  return (c: Call) => {
    if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
    if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(PLAYED)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(SEARCH)) {
      const body: Record<string, unknown> = { items: opts.recentItems ?? [] };
      if (opts.recentCursor) body.next_cursor = opts.recentCursor;
      return { status: 200, body };
    }
    if (c.url.startsWith(PLAYLISTS)) {
      if (c.method === "GET") return { status: 200, body: { items: opts.lists ?? [] } };
      return { status: 200, body: {} };
    }
    return fail(404, "not_found");
  };
}

// addedText is the page's own date format for a created_at, as the Added column writes it.
const addedText = (iso: string) => new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  resetTags();
  session.set(signedAs("olivia"));
  vi.spyOn(Date, "now").mockReturnValue(CLOCK);
});
afterEach(() => {
  stopPlayback();
  setQueueControl(null);
  session.set(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  undoView();
});

describe("the Playlists tab's Recently added list", () => {
  it("is listed second, after Continue watching and before the saved playlists, with its Automatic tag and count", async () => {
    const { calls } = stubFetch(
      answers({ lists: [summary("pls_a", "Evening", 3)], recentItems: [clip("nod_r1", "new.webm"), song("nod_r2", "fresh.mp3")] }),
    );
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(region.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(3));
    const rows = Array.from(region.querySelectorAll('tbody tr[role="row"][aria-label]'));
    expect(rows[0]!.textContent).toContain("Continue watching");
    expect(rows[1]!.textContent).toContain("Recently added");
    expect(rows[1]!.textContent).toContain("Automatic");
    expect(rows[1]!.textContent).toContain("2");
    expect(rows[2]!.textContent).toContain("Evening");
    // The count's first page is read once, even while Continue watching is the chosen list.
    await waitFor(() => expect(calls.filter((c) => c.url.startsWith(SEARCH)).length).toBe(1));
  });

  it("choosing it writes list=recent and sends one search with the newest-first ask of the last 30 days", async () => {
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], recentItems: [clip("nod_r1", "new.webm")] }));
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(row("Recently added"));
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=recent"));
    expect(await screen.findByRole("heading", { name: "Recently added" })).toBeTruthy();
    expect(screen.getByText("Automatic playlist")).toBeTruthy();
    const searches = calls.filter((c) => c.url.startsWith(SEARCH));
    expect(searches.length).toBe(1);
    expect(searches[0]!.url).toBe(`${SEARCH}?${RECENT_QUERY}`);
    // Choosing Continue watching again keeps the address clean, as before.
    fireEvent.click(row("Continue watching"));
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists"));
  });

  it("list=recent opens its detail with the Added date, and no Add files, Rename or Delete", async () => {
    const { calls } = stubFetch(
      answers({ recentItems: [song("nod_r1", "fresh.mp3", { created_at: "2026-11-14T10:00:00Z" }), clip("nod_r2", "new.webm", { created_at: "2026-11-13T09:00:00Z" })] }),
    );
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await waitFor(() => {
      const t = detail.querySelector("table") as HTMLElement;
      expect(within(t).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Title", "#", "Folder", "Added", "Actions"]);
    });
    expect(within(detail).getByRole("row", { name: "fresh.mp3" }).textContent).toContain(addedText("2026-11-14T10:00:00Z"));
    expect(within(detail).getByRole("row", { name: "new.webm" }).textContent).toContain(addedText("2026-11-13T09:00:00Z"));
    expect(screen.queryByRole("button", { name: "Add files" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Rename" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(screen.getByRole("button", { name: "Play" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Shuffle and play" })).toBeTruthy();
    // The audio rows have their lengths asked in batches, as the other rows are.
    await waitFor(() => expect(calls.some((c) => c.url.startsWith(TAGS) && c.url.includes("ids=nod_r1"))).toBe(true));
  });

  it("Play plays the rows in order, and a row click plays from it", async () => {
    stubFetch(answers({ recentItems: [clip("nod_r1", "one.webm"), song("nod_r2", "two.mp3"), clip("nod_r3", "three.webm")] }));
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await within(detail).findByRole("row", { name: "one.webm" });
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_r1");
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_r1", "nod_r2", "nod_r3"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
    fireEvent.click(within(detail).getByRole("row", { name: "three.webm" }));
    expect(getPlayback().seed?.start.id).toBe("nod_r3");
    expect(getPlayback().seed?.selection?.map((n) => n.id)).toEqual(["nod_r1", "nod_r2", "nod_r3"]);
    expect(onPlay).toHaveBeenCalledTimes(2);
  });

  it("the next page is read with the cursor and appends its rows", async () => {
    let n = 0;
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) {
        n++;
        return n === 1
          ? { status: 200, body: { items: [clip("nod_r1", "first.webm")], next_cursor: "rc1" } }
          : { status: 200, body: { items: [song("nod_r2", "second.mp3")] } };
      }
      return answers()(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await within(detail).findByRole("row", { name: "first.webm" });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await within(detail).findByRole("row", { name: "second.mp3" });
    const searches = calls.filter((c) => c.url.startsWith(SEARCH));
    expect(searches.length).toBe(2);
    expect(searches[1]!.url).toBe(`${SEARCH}?${RECENT_QUERY}&cursor=rc1`);
    const rows = Array.from(detail.querySelectorAll('tbody tr[role="row"][aria-label]'));
    expect(rows.map((r) => r.getAttribute("aria-label"))).toEqual(["first.webm", "second.mp3"]);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
  });

  it("an empty answer keeps the table's frame with the empty title and no action", async () => {
    stubFetch(answers({ recentItems: [] }));
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await waitFor(() => {
      const t = detail.querySelector("table") as HTMLElement;
      expect(t).not.toBeNull();
      expect(within(t).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Title", "#", "Folder", "Added"]);
    });
    const body = detail.querySelector("tbody") as HTMLElement;
    expect(await within(body).findByText("Nothing added in the last 30 days")).toBeTruthy();
    expect(body.querySelector(".ant-empty.ant-empty-normal .ant-empty-image")).not.toBeNull();
    expect(body.querySelector(".empty-title")?.textContent).toBe("Nothing added in the last 30 days");
    expect(within(body).queryByRole("button")).toBeNull();
    expect((screen.getByRole("button", { name: "Play" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("a 429 shows the rate-limit text with a retry that reads again", async () => {
    let n = 0;
    stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) {
        n++;
        return n === 1 ? fail(429, "rate_limited") : { status: 200, body: { items: [clip("nod_r1", "new.webm")] } };
      }
      return answers()(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Too many searches. Wait a minute, then try again.");
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("row", { name: "new.webm" })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });
});
