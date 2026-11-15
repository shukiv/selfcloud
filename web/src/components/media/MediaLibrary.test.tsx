// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page's library tabs (mp4): Videos and Music ask the search with their own type filter and page by the
// cursor; Recently played asks the played list and shows the place stopped at. A row's click or Enter plays it in the
// page's session with the loaded rows as the selection. Search off keeps the table's frame with its Empty inside; a
// rate limit shows its text and a retry. Each empty state offers the way to My files.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { getPlayback, stopPlayback } from "../../lib/files/playback";
import { resetTags } from "../../lib/files/tags";
import { session, type Session } from "../../lib/session";
import { expectEmptyTable } from "../../test/emptyTable";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { desktopViewport } from "../../test/viewport";
import MediaLibrary, { type LibraryTab } from "./MediaLibrary";

const SEARCH = "/api/v1/search";
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

function renderLib(tab: LibraryTab, onPlay: () => void = () => {}) {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={["/media"]}>
        <Routes>
          <Route path="/media" element={<MediaLibrary tab={tab} onPlay={onPlay} />} />
          <Route path="/files" element={<p>files page</p>} />
          <Route path="/files/:fld" element={<p>folder page</p>} />
          <Route path="/files/:fld/:dir" element={<p>dir page</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const row = (name: string) => screen.getByRole("row", { name });
const paramsOf = (c: Call) => new URLSearchParams(c.url.split("?")[1] ?? "");

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  resetTags();
  session.set(signedAs("olivia"));
});
afterEach(() => {
  stopPlayback();
  session.set(null);
  vi.unstubAllGlobals();
  undoView();
});

describe("the library's Videos and Music tabs", () => {
  it("Videos asks the search for video files, and lists the name, the folder and the modified date", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) return { status: 200, body: { items: [clip("nod_cccc3333", "Holiday.webm", { place: "Clips" })] } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("videos");
    expect(await screen.findByRole("row", { name: "Holiday.webm" })).toBeTruthy();
    const p = paramsOf(calls.find((c) => c.url.startsWith(SEARCH))!);
    expect(p.get("type")).toBe("video");
    expect(p.get("kind")).toBe("file");
    expect(p.get("limit")).toBe("50");
    expect(p.get("q")).toBeNull();
    expect(row("Holiday.webm").textContent).toContain("Clips");
    expect(row("Holiday.webm").querySelector("time")?.getAttribute("datetime")).toBe("2026-10-02T00:00:00Z");
  });

  it("Music asks the search for audio files", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) return { status: 200, body: { items: [song("nod_ffff6666", "song.mp3")] } };
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("music");
    expect(await screen.findByRole("row", { name: "song.mp3" })).toBeTruthy();
    const p = paramsOf(calls.find((c) => c.url.startsWith(SEARCH))!);
    expect(p.get("type")).toBe("audio");
    expect(p.get("kind")).toBe("file");
  });

  it("pages by the cursor with Load more, keeping the rows already shown", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) {
        if (paramsOf(c).get("cursor") === "c1") return { status: 200, body: { items: [clip("nod_eeee5555", "Second.webm")] } };
        return { status: 200, body: { items: [clip("nod_cccc3333", "First.webm")], next_cursor: "c1" } };
      }
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("videos");
    await screen.findByRole("row", { name: "First.webm" });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("row", { name: "Second.webm" })).toBeTruthy();
    expect(screen.getByRole("row", { name: "First.webm" })).toBeTruthy();
    const searches = calls.filter((c) => c.url.startsWith(SEARCH));
    expect(searches.length).toBe(2);
    expect(paramsOf(searches[1]!).get("cursor")).toBe("c1");
    expect(paramsOf(searches[1]!).get("type")).toBe("video");
    // The list's end: no Load more is left.
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load more" })).toBeNull());
  });

  it("Music shows the tags' title, artist, album and length, and asks the tags in one batch for the rows without any", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(SEARCH))
        return {
          status: 200,
          body: {
            items: [
              song("nod_ffff6666", "song.mp3", { tags: { title: "Song Title", artist: "Artist One", album: "Album X", duration_ms: 65000 } }),
              song("nod_gggg7777", "plain.mp3"),
            ],
          },
        };
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: { nod_gggg7777: { state: "none" } } } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("music");
    await screen.findByRole("row", { name: "song.mp3" });
    expect(row("song.mp3").textContent).toContain("Song Title");
    expect(row("song.mp3").textContent).toContain("Artist One");
    expect(row("song.mp3").textContent).toContain("Album X");
    expect(row("song.mp3").textContent).toContain("1:05");
    expect((row("song.mp3").querySelector('[data-testid="tag-cover"]') as HTMLElement).style.width).toBe("40px");
    // The listing's own tags were kept without a request; the batch asked only the row without any.
    const tagsCalls = calls.filter((c) => c.url.startsWith(TAGS));
    expect(tagsCalls.length).toBe(1);
    expect(tagsCalls[0]!.url).toContain("ids=nod_gggg7777");
    expect(tagsCalls[0]!.url).not.toContain("nod_ffff6666");
    // No tags: the file name stays, and artist, album and length are dashes.
    await waitFor(() => expect(row("plain.mp3").textContent).toContain("—"));
    expect(row("plain.mp3").textContent).toContain("plain.mp3");
    expect(row("plain.mp3").textContent).not.toContain("Artist One");
  });

  it("search off keeps the table's frame and its headers, with the Empty inside", async () => {
    stubFetch((c) => {
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("videos");
    const action = await expectEmptyTable("Videos", {
      headers: ["Name", "Folder", "Modified"],
      title: "The library needs search",
      description: "Search is turned off on this server, so videos and music cannot be listed here.",
    });
    expect(action).toBeNull();
  });

  it("a rate-limited search shows its text and a retry that asks again", async () => {
    let n = 0;
    stubFetch((c) => {
      if (c.url.startsWith(SEARCH)) {
        n++;
        return n === 1 ? fail(429, "rate_limited") : { status: 200, body: { items: [song("nod_ffff6666", "song.mp3")] } };
      }
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("music");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Too many searches. Wait a minute, then try again.");
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("row", { name: "song.mp3" })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });
});

describe("the library's Recently played tab", () => {
  it("asks the played list, and shows the date and the progress, the position alone when the length is 0", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(PLAYED))
        return {
          status: 200,
          body: {
            items: [
              { ...song("nod_hhhh8888", "track.ogg"), position_seconds: 6, duration_seconds: 8, played_at: "2026-11-12T10:00:00Z" },
              { ...clip("nod_iiii9999", "movie.webm"), position_seconds: 65, duration_seconds: 0, played_at: "2026-11-11T10:00:00Z" },
            ],
          },
        };
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("recent");
    expect(await screen.findByRole("row", { name: "track.ogg" })).toBeTruthy();
    expect(row("track.ogg").textContent).toContain("0:06 of 0:08");
    expect(row("track.ogg").querySelector("time")?.getAttribute("datetime")).toBe("2026-11-12T10:00:00Z");
    expect(row("movie.webm").textContent).toContain("1:05");
    expect(row("movie.webm").textContent).not.toContain(" of ");
    const p = paramsOf(calls.find((c) => c.url.startsWith(PLAYED))!);
    expect(p.get("limit")).toBe("50");
    expect(calls.some((c) => c.url.startsWith(SEARCH))).toBe(false);
  });

  it("pages the played list by the cursor too", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.startsWith(PLAYED)) {
        if (paramsOf(c).get("cursor") === "p1")
          return { status: 200, body: { items: [{ ...clip("nod_iiii9999", "second.webm"), position_seconds: 1, duration_seconds: 0, played_at: "2026-11-10T10:00:00Z" }] } };
        return {
          status: 200,
          body: { items: [{ ...song("nod_hhhh8888", "first.ogg"), position_seconds: 6, duration_seconds: 8, played_at: "2026-11-12T10:00:00Z" }], next_cursor: "p1" },
        };
      }
      if (c.url.startsWith(TAGS)) return { status: 200, body: { items: {} } };
      if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderLib("recent");
    await screen.findByRole("row", { name: "first.ogg" });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("row", { name: "second.webm" })).toBeTruthy();
    const played = calls.filter((c) => c.url.startsWith(PLAYED));
    expect(played.length).toBe(2);
    expect(paramsOf(played[1]!).get("cursor")).toBe("p1");
  });
});

describe("playing from the library", () => {
  const answers = (c: Call) => {
    if (c.url.startsWith(SEARCH)) return { status: 200, body: { items: [clip("nod_cccc3333", "One.webm"), clip("nod_dddd4444", "Two.webm")] } };
    if (c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
    return fail(404, "not_found");
  };

  it("a row's click plays it with the loaded rows as the selection and an empty folder, and turns to Now playing", async () => {
    stubFetch(answers);
    const onPlay = vi.fn();
    renderLib("videos", onPlay);
    fireEvent.click(await screen.findByRole("row", { name: "Two.webm" }));
    const seed = getPlayback().seed;
    expect(seed?.start.id).toBe("nod_dddd4444");
    expect(seed?.selection?.map((n) => n.id)).toEqual(["nod_cccc3333", "nod_dddd4444"]);
    expect(seed?.folder.loaded).toEqual([]);
    expect(seed?.folder.hasMore).toBe(false);
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("Enter on the focused row plays it too", async () => {
    stubFetch(answers);
    const onPlay = vi.fn();
    renderLib("videos", onPlay);
    const r = await screen.findByRole("row", { name: "One.webm" });
    fireEvent.focus(r);
    fireEvent.keyDown(r, { key: "Enter" });
    expect(getPlayback().seed?.start.id).toBe("nod_cccc3333");
    expect(onPlay).toHaveBeenCalledTimes(1);
  });

  it("the Play action plays the row, and Open folder goes to the file's folder", async () => {
    stubFetch(answers);
    renderLib("videos");
    const r = await screen.findByRole("row", { name: "One.webm" });
    fireEvent.click(within(r).getByRole("button", { name: "Play: One.webm" }));
    expect(getPlayback().seed?.start.id).toBe("nod_cccc3333");
    stopPlayback();
    fireEvent.click(within(r).getByRole("button", { name: "Open folder: One.webm" }));
    expect(await screen.findByText("dir page")).toBeTruthy();
  });
});

describe("the library's empty states", () => {
  it.each([
    ["videos", "Videos", ["Name", "Folder", "Modified"], "No videos", "Videos you can reach appear here. Add some in My files."],
    ["music", "Music", ["Name", "Artist", "Album", "Length"], "No music", "Songs you can reach appear here. Add some in My files."],
    ["recent", "Recently played", ["Name", "Played", "Progress"], "Nothing played yet", "Play a video or a song and it shows here with the place you stopped at."],
  ] as [LibraryTab, string, string[], string, string][])(
    "%s with nothing found keeps the table's frame and offers the way to My files",
    async (tab, label, headers, title, description) => {
      stubFetch((c) => {
        if (c.url.startsWith(SEARCH) || c.url.startsWith(PLAYED) || c.url.startsWith(TAGS) || c.url.startsWith(FOLDERS)) return { status: 200, body: { items: [] } };
        return fail(404, "not_found");
      });
      renderLib(tab);
      const action = await expectEmptyTable(label, { headers, title, description, action: "Go to My files" });
      fireEvent.click(action!);
      expect(await screen.findByText("files page")).toBeTruthy();
    },
  );
});
