// SPDX-License-Identifier: AGPL-3.0-or-later

// A saved playlist put in the queue ends at its own end. The folder the player started from has more pages, but the
// playlist does not go on to them (review of slice x4): the next page is not asked for, and playback stops.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Pager, type PageLike } from "../../lib/files/pager";
import * as playlists from "../../lib/files/playlists";
import type { Node } from "../../lib/files/types";
import MediaPlaylist from "./MediaPlaylist";

vi.mock("../../lib/files/playlists", async (orig) => {
  const real = await orig<typeof import("../../lib/files/playlists")>();
  return { ...real, listPlaylists: vi.fn(), getPlaylist: vi.fn() };
});

function file(name: string): Node {
  return {
    id: name, folder_id: "f", parent_id: "d", name, kind: "file", size: 1, mime_type: "video/webm",
    created_at: "2026-10-31T00:00:00Z", modified_at: "2026-10-31T00:00:00Z", role: "editor", no_download: false, editors_can_share: false,
  } as unknown as Node;
}

const linkTo = (node: Node) => ({ url: `https://content.example/${node.id}` });
const playing = (container: HTMLElement) => container.querySelector("video, audio")?.getAttribute("src") ?? null;

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("a saved playlist in the player", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
    vi.mocked(playlists.listPlaylists).mockResolvedValue([{ id: "pls_1", name: "Evening", entry_count: 1, created_at: "", updated_at: "" }]);
    vi.mocked(playlists.getPlaylist).mockResolvedValue({
      id: "pls_1", name: "Evening", entry_count: 1, created_at: "", updated_at: "",
      entries: [{ entry_id: "ple_1", position: 0, node_id: "x.webm", unavailable: false, node: file("x.webm") }],
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("ends at its own end: the folder's next page is not read", async () => {
    const a = file("a.webm");
    const fetchPage = vi.fn(async (): Promise<PageLike> => ({ items: [file("b.webm")] }));
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const folder = new Pager([a], "c1", fetchPage);
    const { container } = render(
      <App>
        <MediaPlaylist start={a} folder={folder} fetchLink={fetchLink} saved />
      </App>,
    );
    await waitFor(() => expect(playing(container)).toBe("https://content.example/a.webm"));
    fireEvent.click(await screen.findByRole("button", { name: "Play: Evening" }));
    await waitFor(() => expect(playlists.getPlaylist).toHaveBeenCalledWith("pls_1"));
    // Play starts the saved list's one file. The folder has a page more: when that file ends, the list ends with it.
    await waitFor(() => expect(playing(container)).toBe("https://content.example/x.webm"));
    fireEvent.ended(container.querySelector("video, audio") as HTMLMediaElement);
    await flush();
    await flush();
    expect(fetchPage).not.toHaveBeenCalled();
  });
});
