// SPDX-License-Identifier: AGPL-3.0-or-later

// The Playlists tab's second automatic list, Recently added: the person's video and audio files created in the last 30 days.
// The server's search takes one type per ask, so the list reads two searches at once (type=video and type=audio, each
// kind=file&order=newest&created_after&limit=50) and keeps the 50 newest of the union, by created_at, ties by id. There is no
// paging: when either answer has a next page, one line under the table says so. The left table lists it right after Continue
// watching; the address's list=recent picks its detail, which plays its rows and shows the added date instead of the length,
// with no Add files, Rename or Delete. The API is replaced at the fetch boundary, and the clock is fixed so created_after is known.

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
const VIDEO_QUERY = `type=video&kind=file&order=newest&created_after=${AFTER}&limit=50`;
const AUDIO_QUERY = `type=audio&kind=file&order=newest&created_after=${AFTER}&limit=50`;

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

// answers keeps the playlists, the played list, the tags and the folders empty, and gives each type's search what a check
// names for it, with a next page when the check asks for one.
function answers(opts: { lists?: unknown[]; videoItems?: unknown[]; audioItems?: unknown[]; videoCursor?: string; audioCursor?: string } = {}) {
  return (c: Call) => {
    if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
    if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(PLAYED)) return { status: 200, body: { items: [] } };
    if (c.url.startsWith(SEARCH)) {
      const video = new URLSearchParams(c.url.split("?")[1] ?? "").get("type") === "video";
      const body: Record<string, unknown> = { items: (video ? opts.videoItems : opts.audioItems) ?? [] };
      const cursor = video ? opts.videoCursor : opts.audioCursor;
      if (cursor) body.next_cursor = cursor;
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

const bodyRows = (region: HTMLElement) => Array.from(region.querySelectorAll('tbody tr[role="row"][aria-label]'));

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
      answers({ lists: [summary("pls_a", "Evening", 3)], videoItems: [clip("nod_r1", "new.webm")], audioItems: [song("nod_r2", "fresh.mp3")] }),
    );
    renderTab();
    const region = await screen.findByRole("region", { name: "Your playlists" });
    await waitFor(() => expect(bodyRows(region).length).toBe(3));
    const rows = bodyRows(region);
    expect(rows[0]!.textContent).toContain("Continue watching");
    expect(rows[1]!.textContent).toContain("Recently added");
    expect(rows[1]!.textContent).toContain("Automatic");
    expect(rows[1]!.textContent).toContain("2");
    expect(rows[2]!.textContent).toContain("Evening");
    // The count's two searches are read once, even while Continue watching is the chosen list.
    await waitFor(() => expect(calls.filter((c) => c.url.startsWith(SEARCH)).length).toBe(2));
  });

  it("choosing it writes list=recent and sends two searches, one per type, with the newest-first ask of the last 30 days", async () => {
    const { calls } = stubFetch(answers({ lists: [summary("pls_a", "Evening", 1)], videoItems: [clip("nod_r1", "new.webm")] }));
    renderTab();
    await screen.findByRole("region", { name: "Your playlists" });
    fireEvent.click(row("Recently added"));
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists&list=recent"));
    expect(await screen.findByRole("heading", { name: "Recently added" })).toBeTruthy();
    expect(screen.getByText("Automatic playlist")).toBeTruthy();
    const searches = calls.filter((c) => c.url.startsWith(SEARCH)).map((c) => c.url);
    expect(searches.length).toBe(2);
    expect(searches).toContain(`${SEARCH}?${VIDEO_QUERY}`);
    expect(searches).toContain(`${SEARCH}?${AUDIO_QUERY}`);
    // Choosing Continue watching again keeps the address clean, as before.
    fireEvent.click(row("Continue watching"));
    await waitFor(() => expect(screen.getByTestId("pl-loc").textContent).toBe("?tab=playlists"));
  });

  it("merges the two answers newest first by created_at, ties by id", async () => {
    stubFetch(
      answers({
        videoItems: [
          clip("nod_v1", "v-new.webm", { created_at: "2026-11-14T10:00:00Z" }),
          clip("nod_v2", "v-old.webm", { created_at: "2026-11-12T10:00:00Z" }),
          clip("nod_t1", "tie-v.webm", { created_at: "2026-11-13T10:00:00Z" }),
        ],
        audioItems: [
          song("nod_a1", "a-mid.mp3", { created_at: "2026-11-13T10:00:00Z" }),
          song("nod_a2", "a-old.mp3", { created_at: "2026-11-11T10:00:00Z" }),
        ],
      }),
    );
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await within(detail).findByRole("row", { name: "v-new.webm" });
    await waitFor(() => expect(bodyRows(detail).map((r) => r.getAttribute("aria-label"))).toEqual(["v-new.webm", "a-mid.mp3", "tie-v.webm", "v-old.webm", "a-old.mp3"]));
    // Neither answer has a next page, so no note sits under the table, and there is no Load more.
    expect(screen.queryByText("Showing the 50 newest.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("keeps the 50 newest of the union and says so when either answer has a next page", async () => {
    const base = Date.UTC(2026, 10, 14, 12, 0, 0);
    const videoItems = Array.from({ length: 50 }, (_, i) => clip(`nod_v${i}`, `v${i}.webm`, { created_at: new Date(base - i * 3600000).toISOString() }));
    const audioItems = [song("nod_a0", "a-new.mp3", { created_at: new Date(base + 3600000).toISOString() })];
    const { calls } = stubFetch(answers({ videoItems, audioItems, videoCursor: "vc1" }));
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await within(detail).findByRole("row", { name: "a-new.mp3" });
    await waitFor(() => expect(bodyRows(detail).length).toBe(50));
    const rows = bodyRows(detail);
    expect(rows[0]!.getAttribute("aria-label")).toBe("a-new.mp3");
    expect(rows[49]!.getAttribute("aria-label")).toBe("v48.webm");
    expect(within(detail).queryByRole("row", { name: "v49.webm" })).toBeNull();
    expect(await screen.findByText("Showing the 50 newest.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    expect(calls.filter((c) => c.url.startsWith(SEARCH)).length).toBe(2);
  });

  it("list=recent opens its detail with the Added date, and no Add files, Rename or Delete", async () => {
    const { calls } = stubFetch(
      answers({
        videoItems: [clip("nod_r2", "new.webm", { created_at: "2026-11-13T09:00:00Z" })],
        audioItems: [song("nod_r1", "fresh.mp3", { created_at: "2026-11-14T10:00:00Z" })],
      }),
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

  it("Play plays the merged rows in order, and a row click plays from it", async () => {
    stubFetch(
      answers({
        videoItems: [clip("nod_v1", "one.webm", { created_at: "2026-11-14T10:00:00Z" }), clip("nod_v2", "three.webm", { created_at: "2026-11-12T10:00:00Z" })],
        audioItems: [song("nod_a1", "two.mp3", { created_at: "2026-11-13T10:00:00Z" })],
      }),
    );
    const onPlay = vi.fn();
    renderTab(onPlay, "/media?tab=playlists&list=recent");
    const detail = await screen.findByRole("region", { name: "Recently added" });
    await within(detail).findByRole("row", { name: "one.webm" });
    fireEvent.click(screen.getByRole("button", { name: "Play" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_v1");
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_v1", "nod_a1", "nod_v2"]);
    expect(onPlay).toHaveBeenCalledTimes(1);
    fireEvent.click(within(detail).getByRole("row", { name: "three.webm" }));
    expect(getPlayback().seed?.start.id).toBe("nod_v2");
    expect(getPlayback().seed?.selection?.map((n) => n.id)).toEqual(["nod_v1", "nod_a1", "nod_v2"]);
    expect(onPlay).toHaveBeenCalledTimes(2);
  });

  it("an empty answer keeps the table's frame with the empty title and no action", async () => {
    stubFetch(answers());
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

  it("a 429 in one search shows the rate-limit text with a retry, and no half list", async () => {
    let n = 0;
    stubFetch((c) => {
      if (c.url.startsWith(SEARCH) && c.url.includes("type=video")) {
        n++;
        return n === 1 ? fail(429, "rate_limited") : { status: 200, body: { items: [clip("nod_r1", "new.webm")] } };
      }
      return answers({ audioItems: [song("nod_r2", "fresh.mp3")] })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Too many searches. Wait a minute, then try again.");
    expect(screen.queryByRole("row", { name: "fresh.mp3" })).toBeNull();
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("row", { name: "new.webm" })).toBeTruthy();
    expect(screen.getByRole("row", { name: "fresh.mp3" })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("a failed search shows the list's error with a retry, and no half list", async () => {
    let n = 0;
    stubFetch((c) => {
      if (c.url.startsWith(SEARCH) && c.url.includes("type=audio")) {
        n++;
        return n === 1 ? fail(500, "internal") : { status: 200, body: { items: [song("nod_r2", "fresh.mp3")] } };
      }
      return answers({ videoItems: [clip("nod_r1", "new.webm")] })(c);
    });
    renderTab(() => {}, "/media?tab=playlists&list=recent");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("That did not work. Try again.");
    expect(screen.queryByRole("row", { name: "new.webm" })).toBeNull();
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("row", { name: "new.webm" })).toBeTruthy();
    expect(screen.getByRole("row", { name: "fresh.mp3" })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });
});
