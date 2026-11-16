// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page (mp1): one tab for the running session, the hosts the session draws into, and the empty state with the
// way back to My files. The page attaches its hosts in a layout effect, so the detach runs before the hosts leave the
// page, and StrictMode's double mount does not end the session.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getPlayback, startPlayback, stopPlayback } from "../lib/files/playback";
import { Pager } from "../lib/files/pager";
import type { Node } from "../lib/files/types";
import { createQueryClient } from "../lib/query";
import { session } from "../lib/session";
import { fail, stubFetch } from "../test/fetchStub";
import MediaPage from "./MediaPage";

const audio = { id: "n-a", name: "a.ogg", kind: "file", mime_type: "audio/ogg", size: 1 } as unknown as Node;
const seed = { start: audio, folder: new Pager([audio], null, async () => ({ items: [] })) };

// The address the page is at, for the tab checks: the tab is written with replace, and playing turns back to Now playing.
function LocationProbe() {
  const loc = useLocation();
  return <span data-testid="media-loc">{loc.search}</span>;
}

function renderPage(strict = false, entry = "/media") {
  const page = (
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route
            path="/media"
            element={
              <>
                <MediaPage />
                <LocationProbe />
              </>
            }
          />
          <Route path="/files" element={<p>files page</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
  return render(strict ? <StrictMode>{page}</StrictMode> : page);
}

// The library tabs read the search and the played list; these answers keep them empty but for the rows a check asks for.
function stubLibrary(items: unknown[] = []) {
  return stubFetch((c) => {
    if (c.url.startsWith("/api/v1/search")) return { status: 200, body: { items } };
    if (c.url.startsWith("/api/v1/media/played")) return { status: 200, body: { items: [] } };
    if (c.url.startsWith("/api/v1/media/tags")) return { status: 200, body: { items: {} } };
    if (c.url.startsWith("/api/v1/folders")) return { status: 200, body: { items: [] } };
    return fail(404, "not_found");
  });
}

afterEach(async () => {
  stopPlayback();
  session.set(null);
  await Promise.resolve();
});

describe("the Media page", () => {
  it("says nothing is playing, and offers the way to My files", async () => {
    renderPage();
    expect(await screen.findByText("Nothing is playing")).toBeTruthy();
    expect(screen.getByText("Play a video or a song from your files and it shows here.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Go to My files" }));
    expect(await screen.findByText("files page")).toBeTruthy();
  });

  it("attaches its hosts on mount, and detaches them on unmount", async () => {
    const view = renderPage();
    await screen.findByText("Nothing is playing");
    const page = getPlayback().page;
    expect(page).not.toBeNull();
    expect(page!.stage).toBe(screen.getByTestId("media-page-stage"));
    expect(page!.queue).toBe(screen.getByTestId("media-page-queue"));
    view.unmount();
    expect(getPlayback().page).toBeNull();
    await Promise.resolve();
  });

  it("keeps the hosts mounted while a session runs, with no empty state", async () => {
    startPlayback(seed);
    renderPage();
    await waitFor(() => expect(getPlayback().page).not.toBeNull());
    expect(screen.queryByText("Nothing is playing")).toBeNull();
    expect(screen.getByTestId("media-page-stage")).toBeTruthy();
    expect(screen.getByTestId("media-page-queue")).toBeTruthy();
  });

  it("survives StrictMode's double effect: the mount does not end the session", async () => {
    const told = vi.fn();
    startPlayback(seed, told);
    renderPage(true);
    await waitFor(() => expect(getPlayback().page).not.toBeNull());
    await act(async () => {
      await Promise.resolve();
    });
    expect(getPlayback().seed).not.toBeNull();
    expect(told).not.toHaveBeenCalled();
  });

  it("?tab= opens that tab, and an unknown value falls back to Now playing", async () => {
    const { calls } = stubLibrary();
    renderPage(false, "/media?tab=music");
    const music = await screen.findByRole("tab", { name: "Music" });
    expect(music.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByTestId("media-loc").textContent).toBe("?tab=music");
    // The Music tab asked the search for audio files.
    await waitFor(() => expect(calls.some((c) => c.url.startsWith("/api/v1/search") && c.url.includes("type=audio"))).toBe(true));
  });

  it("an unknown tab value opens Now playing", async () => {
    stubLibrary();
    renderPage(false, "/media?tab=bogus");
    expect(await screen.findByText("Nothing is playing")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Now playing" }).getAttribute("aria-selected")).toBe("true");
  });

  it("changing tabs keeps the stage and queue hosts mounted, the same nodes, and the session running", async () => {
    stubLibrary();
    startPlayback(seed);
    renderPage();
    await screen.findByRole("tab", { name: "Music" });
    const stage = screen.getByTestId("media-page-stage");
    const queue = screen.getByTestId("media-page-queue");
    const page = getPlayback().page;
    expect(page).not.toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Music" }));
    expect(await screen.findByRole("region", { name: "Music" })).toBeTruthy();
    expect(screen.getByTestId("media-page-stage")).toBe(stage);
    expect(screen.getByTestId("media-page-queue")).toBe(queue);
    expect(getPlayback().page).toBe(page);
    expect(getPlayback().seed).not.toBeNull();
    expect(screen.getByTestId("media-loc").textContent).toBe("?tab=music");

    fireEvent.click(screen.getByRole("tab", { name: "Videos" }));
    expect(await screen.findByRole("region", { name: "Videos" })).toBeTruthy();
    expect(screen.getByTestId("media-page-stage")).toBe(stage);
    expect(screen.getByTestId("media-page-queue")).toBe(queue);
    expect(getPlayback().page).toBe(page);

    fireEvent.click(screen.getByRole("tab", { name: "Now playing" }));
    await waitFor(() => expect(screen.getByTestId("media-loc").textContent).toBe(""));
    expect(screen.getByTestId("media-page-stage")).toBe(stage);
    expect(screen.getByTestId("media-page-queue")).toBe(queue);
  });

  it("the hosts are mounted even when a library tab is the address from the first load", async () => {
    stubLibrary();
    startPlayback(seed);
    renderPage(false, "/media?tab=recent");
    expect(await screen.findByRole("region", { name: "Recently played" })).toBeTruthy();
    const page = getPlayback().page;
    expect(page).not.toBeNull();
    expect(page!.stage).toBe(screen.getByTestId("media-page-stage"));
    expect(page!.queue).toBe(screen.getByTestId("media-page-queue"));
  });

  it("playing a library row turns the page back to Now playing", async () => {
    stubLibrary([audio]);
    renderPage(false, "/media?tab=music");
    const r = await screen.findByRole("row", { name: "a.ogg" });
    fireEvent.click(r);
    expect(getPlayback().seed?.start.id).toBe("n-a");
    await waitFor(() => expect(screen.getByRole("tab", { name: "Now playing" }).getAttribute("aria-selected")).toBe("true"));
    expect(screen.getByTestId("media-loc").textContent).toBe("");
  });
});

// stubPlaylists answers the Playlists tab's reads: the saved playlists, the played list, the tags and the folders.
function stubPlaylists() {
  return stubFetch((c) => {
    if (c.url.startsWith("/api/v1/playlists")) return { status: 200, body: { items: [] } };
    if (c.url.startsWith("/api/v1/media/played")) return { status: 200, body: { items: [] } };
    if (c.url.startsWith("/api/v1/media/tags")) return { status: 200, body: { items: {} } };
    if (c.url.startsWith("/api/v1/folders")) return { status: 200, body: { items: [] } };
    if (c.url.startsWith("/api/v1/search")) return { status: 200, body: { items: [] } };
    return fail(404, "not_found");
  });
}

describe("the Media page's Playlists tab", () => {
  it("sits right after Now playing, before the library tabs", async () => {
    stubPlaylists();
    renderPage();
    await screen.findByRole("tab", { name: "Playlists" });
    const labels = screen.getAllByRole("tab").map((t) => t.textContent);
    expect(labels).toEqual(["Now playing", "Playlists", "Videos", "Music", "Recently played"]);
  });

  it("?tab=playlists opens it, and its list is drawn", async () => {
    stubPlaylists();
    renderPage(false, "/media?tab=playlists");
    const tab = await screen.findByRole("tab", { name: "Playlists" });
    expect(tab.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByTestId("media-loc").textContent).toBe("?tab=playlists");
    expect(await screen.findByRole("region", { name: "Your playlists" })).toBeTruthy();
  });

  it("keeps the stage and queue hosts mounted while the Playlists tab is open", async () => {
    stubPlaylists();
    startPlayback(seed);
    renderPage(false, "/media?tab=playlists");
    await screen.findByRole("region", { name: "Your playlists" });
    const page = getPlayback().page;
    expect(page).not.toBeNull();
    expect(page!.stage).toBe(screen.getByTestId("media-page-stage"));
    expect(page!.queue).toBe(screen.getByTestId("media-page-queue"));
    expect(getPlayback().seed).not.toBeNull();
  });
});
