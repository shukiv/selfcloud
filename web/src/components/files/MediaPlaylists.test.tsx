// SPDX-License-Identifier: AGPL-3.0-or-later

// The saved-playlists section (decision 58). A first load that fails shows an error with a retry; a refresh that fails keeps
// the list on screen; a save makes one playlist however often it is pressed, and a failed save makes none; a move is sent
// once; the newest Play wins; an editor whose first read fails can be read again. The API is replaced; the rendering and
// the messages are the real ones.

import { App } from "antd";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import MediaPlaylists from "./MediaPlaylists";
import * as playlists from "../../lib/files/playlists";
import type { Entry } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";

vi.mock("../../lib/files/playlists", async (orig) => {
  const real = await orig<typeof import("../../lib/files/playlists")>();
  return {
    ...real,
    listPlaylists: vi.fn(),
    deletePlaylist: vi.fn(),
    createPlaylist: vi.fn(),
    getPlaylist: vi.fn(),
    reorderEntries: vi.fn(),
    removeEntry: vi.fn(),
    renamePlaylist: vi.fn(),
    replaceEntries: vi.fn(),
  };
});

const summary = { id: "pls_1", name: "Evening", entry_count: 3, created_at: "", updated_at: "" };

function file(id: string): Node {
  return { id, folder_id: "f", parent_id: "d", name: `${id}.webm`, kind: "file", size: 1, mime_type: "video/webm",
    created_at: "", modified_at: "", no_download: false, editors_can_share: false } as unknown as Node;
}

function detailOf(pls: string, ids: string[]) {
  return {
    id: pls, name: "Evening", entry_count: ids.length, created_at: "", updated_at: "",
    entries: ids.map((id, i) => ({ entry_id: `ple_${id}`, position: i, node_id: id, unavailable: false, node: file(id) })),
  };
}

// flush lets the pending promises of the section run.
async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

function renderSection(opts: { onOpen?: (entries: Entry[]) => void; queue?: Entry[] } = {}) {
  return render(
    <App>
      <MediaPlaylists queue={opts.queue ?? []} onOpen={opts.onOpen ?? vi.fn()} />
    </App>,
  );
}

const queueOfOne: Entry[] = [{ node: file("nod_a"), mark: "ok" }];

describe("MediaPlaylists", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("shows an error with a retry when the first load fails", async () => {
    vi.mocked(playlists.listPlaylists).mockRejectedValueOnce(new Error("down")).mockResolvedValueOnce([summary]);
    renderSection();
    expect(await screen.findByText("The saved playlists could not be loaded.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Evening")).toBeTruthy();
  });

  it("keeps the list on screen when a refresh fails", async () => {
    vi.mocked(playlists.listPlaylists).mockResolvedValueOnce([summary]).mockRejectedValueOnce(new Error("down"));
    vi.mocked(playlists.deletePlaylist).mockResolvedValueOnce(undefined);
    renderSection();
    expect(await screen.findByText("Evening")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Delete: Evening" }));
    // The confirmation's own OK button; the row's delete button is named "Delete: Evening".
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(playlists.deletePlaylist).toHaveBeenCalledWith("pls_1"));
    await waitFor(() => expect(playlists.listPlaylists).toHaveBeenCalledTimes(2));
    expect(screen.queryByText("The saved playlists could not be loaded.")).toBeNull();
    expect(screen.getByText("Evening")).toBeTruthy();
  });

  it("makes one playlist however often Enter is pressed while it is saved", async () => {
    let finish!: (s: typeof summary) => void;
    vi.mocked(playlists.listPlaylists).mockResolvedValue([]);
    vi.mocked(playlists.createPlaylist).mockImplementation(() => new Promise((res) => (finish = res)));
    renderSection({ queue: queueOfOne });
    await flush();
    fireEvent.click(await screen.findByRole("button", { name: "Save queue" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "Evening" } });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter", keyCode: 13 });
    await act(async () => finish(summary));
    expect(playlists.createPlaylist).toHaveBeenCalledTimes(1);
    expect(playlists.createPlaylist).toHaveBeenCalledWith("Evening", ["nod_a"]);
  });

  it("a save that fails makes no playlist, so the retry makes the only one", async () => {
    vi.mocked(playlists.listPlaylists).mockResolvedValue([]);
    vi.mocked(playlists.createPlaylist)
      .mockRejectedValueOnce(new Error("refused"))
      .mockResolvedValueOnce(summary);
    renderSection({ queue: queueOfOne });
    await flush();
    fireEvent.click(await screen.findByRole("button", { name: "Save queue" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "Evening" } });
    // The dialog's own OK button (its text is "Save"); selected in the footer, since the page has other buttons.
    const ok = () => document.querySelector(".ant-modal-footer .ant-btn-primary") as HTMLButtonElement;
    fireEvent.click(ok());
    await waitFor(() => expect(playlists.createPlaylist).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(ok().disabled).toBe(false));
    fireEvent.click(ok());
    await waitFor(() => expect(playlists.createPlaylist).toHaveBeenCalledTimes(2));
    expect(playlists.createPlaylist).toHaveBeenLastCalledWith("Evening", ["nod_a"]);
  });

  it("sends one move when its button is pressed twice before the first answer", async () => {
    vi.mocked(playlists.listPlaylists).mockResolvedValue([summary]);
    vi.mocked(playlists.getPlaylist).mockResolvedValue(detailOf("pls_1", ["a", "b", "c"]));
    let answer!: () => void;
    vi.mocked(playlists.reorderEntries).mockImplementation(() => new Promise<void>((res) => (answer = res)));
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "Edit: Evening" }));
    const down = await screen.findByRole("button", { name: "Move down: a.webm" });
    fireEvent.click(down);
    fireEvent.click(down);
    await act(async () => answer());
    expect(playlists.reorderEntries).toHaveBeenCalledTimes(1);
    // Moving a down once gives b, a, c.
    expect(playlists.reorderEntries).toHaveBeenCalledWith("pls_1", ["ple_b", "ple_a", "ple_c"]);
  });

  it("the newest Play wins when the earlier one answers last", async () => {
    const older = { id: "pls_a", name: "Old", entry_count: 1, created_at: "", updated_at: "" };
    const newer = { id: "pls_b", name: "New", entry_count: 1, created_at: "", updated_at: "" };
    vi.mocked(playlists.listPlaylists).mockResolvedValue([older, newer]);
    let answerOld!: (d: ReturnType<typeof detailOf>) => void;
    vi.mocked(playlists.getPlaylist).mockImplementation((pls: string) =>
      pls === "pls_a" ? new Promise((res) => (answerOld = res)) : Promise.resolve(detailOf("pls_b", ["b_file"])),
    );
    const onOpen = vi.fn();
    renderSection({ onOpen });
    fireEvent.click(await screen.findByRole("button", { name: "Play: Old" }));
    fireEvent.click(await screen.findByRole("button", { name: "Play: New" }));
    await waitFor(() => expect(onOpen).toHaveBeenCalledTimes(1));
    await act(async () => answerOld(detailOf("pls_a", ["a_file"])));
    await flush();
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen.mock.calls[0][0][0].node.id).toBe("b_file");
  });

  it("an editor whose first read fails can be read again", async () => {
    vi.mocked(playlists.listPlaylists).mockResolvedValue([summary]);
    vi.mocked(playlists.getPlaylist).mockRejectedValueOnce(new Error("down")).mockResolvedValueOnce(detailOf("pls_1", ["a", "b"]));
    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "Edit: Evening" }));
    expect(await screen.findByText("The saved playlists could not be loaded.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.getAllByTestId("playlist-entry")).toHaveLength(2));
  });
});
