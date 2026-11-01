// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import { Pager, type PageLike } from "../../lib/files/pager";
import type { Node } from "../../lib/files/types";
import MediaPlaylist, { SKIP_MS } from "./MediaPlaylist";

function file(name: string, extra: Partial<Node> = {}): Node {
  return {
    id: name,
    folder_id: "f",
    parent_id: "d",
    name,
    kind: "file",
    size: 1,
    mime_type: name.endsWith(".mp3") ? "audio/mpeg" : "video/webm",
    created_at: "2026-10-31T00:00:00Z",
    modified_at: "2026-10-31T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

// linkTo answers a link for a file: its address names the file, so a test can see which file the player was given.
const linkTo = (node: Node) => ({ url: `https://content.example/${node.id}` });

// flush lets the pending promises of the playlist run.
async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

// playing is the source of the video or audio element on screen, or null.
const playing = (container: HTMLElement) => container.querySelector("video, audio")?.getAttribute("src") ?? null;

describe("MediaPlaylist", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("plays the next file when the one on screen ends, asking for a link to that file only", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    expect(playing(container)).toBe("https://content.example/a.webm");
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(playing(container)).toBe("https://content.example/b.webm");
    expect(fetchLink.mock.calls.map((c) => c[0].id)).toEqual(["a.webm", "b.webm"]);
  });

  it("reads the next page of the folder only when the playlist reaches its end", async () => {
    const a = file("a.webm");
    const pages: Record<string, PageLike> = { c1: { items: [file("b.webm"), file("c.webm")], next_cursor: "c2" }, c2: { items: [file("d.webm")] } };
    const fetchPage = vi.fn(async (c: string) => pages[c]);
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const folder = new Pager([a], "c1", fetchPage);
    const { container } = render(<MediaPlaylist start={a} folder={folder} fetchLink={fetchLink} />);
    await flush();
    expect(fetchPage).not.toHaveBeenCalled();
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(playing(container)).toBe("https://content.example/b.webm");
    // c is still in the first page: its end asks for no page. Only the end of the last file read asks for the next one.
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(playing(container)).toBe("https://content.example/c.webm");
    expect(fetchPage).toHaveBeenCalledTimes(1);
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    await flush();
    expect(fetchPage.mock.calls.map((c) => c[0])).toEqual(["c1", "c2"]);
    expect(playing(container)).toBe("https://content.example/d.webm");
  });

  it("marks a file it could not play, skips it after the pause, and says so when nothing plays", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const files = [file("a.webm"), file("b.webm"), file("c.webm")];
    // No file plays: every one of them is refused, so the run of failures reaches the end of the list.
    const fetchLink = vi.fn(async (_n: Node): Promise<{ url: string }> => {
      throw new ApiError(500, "internal", {}, 0);
    });
    render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // a failed at once, and its message is on screen during the pause.
    expect(screen.getByRole("alert").textContent).toContain("Skipping");
    await act(async () => {
      vi.advanceTimersByTime(SKIP_MS + 10);
    });
    await flush();
    expect(fetchLink.mock.calls.map((c) => c[0].id)).toEqual(["a.webm", "b.webm"]);
    await act(async () => {
      vi.advanceTimersByTime(SKIP_MS + 10);
    });
    await flush();
    expect(fetchLink.mock.calls.map((c) => c[0].id)).toEqual(["a.webm", "b.webm", "c.webm"]);
    // Nothing remains to play: playback stops with the message, and asks for no more links.
    await act(async () => {
      vi.advanceTimersByTime(SKIP_MS + 10);
    });
    await flush();
    expect(fetchLink).toHaveBeenCalledTimes(3);
    expect(screen.getByRole("status").textContent).toContain("could be played");
  });

  it("plays a file the person may not download: its player link is not a download, so it is asked for and plays", async () => {
    const a = file("a.webm");
    const b = file("b.webm", { no_download: true, role: "viewer" });
    const c = file("c.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b, c], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(playing(container)).toBe("https://content.example/b.webm");
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(playing(container)).toBe("https://content.example/c.webm");
    expect(fetchLink.mock.calls.map((x) => x[0].id)).toEqual(["a.webm", "b.webm", "c.webm"]);
  });

  it("starts on a file the person may not download, and plays it", async () => {
    const a = file("a.webm", { no_download: true, role: "viewer" });
    const b = file("b.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    expect(fetchLink.mock.calls.map((x) => x[0].id)).toEqual(["a.webm"]);
    expect(playing(container)).toBe("https://content.example/a.webm");
  });

  it("removes the file on screen and plays the one after it", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // Remove is in the row's More menu.
    fireEvent.click(screen.getByRole("button", { name: "Actions for a.webm" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Remove a\.webm/ }));
    await flush();
    expect(playing(container)).toBe("https://content.example/b.webm");
  });

  it("loops the file on screen with the element's own loop when repeat is one", async () => {
    const a = file("a.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: /^Repeat/ }));
    fireEvent.click(screen.getByRole("button", { name: /^Repeat/ }));
    await flush();
    expect((container.querySelector("video") as HTMLVideoElement).loop).toBe(true);
  });

  it("stops at the end, without a file after the last, when repeat is off", async () => {
    const a = file("a.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(fetchLink).toHaveBeenCalledTimes(1);
    expect(screen.getByText("The playlist has ended.")).toBeTruthy();
  });

  it("ends the playlist with the owner's answer when the credential is refused, and plays nothing more", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    // The share's own check says it is gone: the playlist stops with the lost message.
    const onLost = vi.fn(async () => true);
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "a.webm") return linkTo(n);
      throw new ApiError(401, "unauthorized", {}, 0);
    });
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} onLost={onLost} />);
    await flush();
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(onLost).toHaveBeenCalledTimes(1);
    expect(fetchLink).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("alert").textContent).toContain("no longer available");
  });
});
