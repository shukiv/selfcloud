// SPDX-License-Identifier: AGPL-3.0-or-later

// The Media page (mp1): one tab for the running session, the hosts the session draws into, and the empty state with the
// way back to My files. The page attaches its hosts in a layout effect, so the detach runs before the hosts leave the
// page, and StrictMode's double mount does not end the session.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getPlayback, startPlayback, stopPlayback } from "../lib/files/playback";
import { Pager } from "../lib/files/pager";
import type { Node } from "../lib/files/types";
import { session } from "../lib/session";
import MediaPage from "./MediaPage";

const audio = { id: "n-a", name: "a.ogg", kind: "file", mime_type: "audio/ogg", size: 1 } as unknown as Node;
const seed = { start: audio, folder: new Pager([audio], null, async () => ({ items: [] })) };

function renderPage(strict = false) {
  const page = (
    <MemoryRouter initialEntries={["/media"]}>
      <Routes>
        <Route path="/media" element={<MediaPage />} />
        <Route path="/files" element={<p>files page</p>} />
      </Routes>
    </MemoryRouter>
  );
  return render(strict ? <StrictMode>{page}</StrictMode> : page);
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
    expect(page!.stage).toBe(screen.getByTestId("media-stage"));
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
    expect(screen.getByTestId("media-stage")).toBeTruthy();
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
});
