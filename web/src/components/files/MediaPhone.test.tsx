// SPDX-License-Identifier: AGPL-3.0-or-later

// The phone player's own parts (mp7): the one centred line "{n}. {artist} - {title}", the large transport that drives the
// same element and the same navigation as the bar, and the action row (Playlist, Download when allowed, More with Speed,
// Shuffle, Repeat, Details and Open folder). The parts are drawn only at phone width; their owner decides that.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import { resetTags, seedTags } from "../../lib/files/tags";
import { folders, hiddenFolders } from "../../lib/files/folders";
import type { Node } from "../../lib/files/types";
import MediaPhone from "./MediaPhone";

function file(name: string, extra: Partial<Node> = {}): Node {
  return {
    id: `nod_${name}`,
    folder_id: "f1",
    parent_id: "d1",
    name,
    kind: "file",
    size: 1,
    mime_type: "audio/mpeg",
    created_at: "",
    modified_at: "",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    ...extra,
  } as unknown as Node;
}

// probe reads the address the router holds, so a navigation is seen where it lands.
function Probe() {
  const loc = useLocation();
  return <div data-testid="probe">{loc.pathname}</div>;
}

function props(node: Node, over: Partial<Parameters<typeof MediaPhone>[0]> = {}) {
  return {
    node,
    position: 1,
    media: null as HTMLMediaElement | null,
    playing: false,
    nav: { next: vi.fn(), previous: vi.fn() },
    onPress: vi.fn(),
    onPlaylist: vi.fn(),
    onDownload: vi.fn(),
    onDetails: vi.fn(),
    shuffle: false,
    repeat: "off" as const,
    onShuffle: vi.fn(),
    onRepeat: vi.fn(),
    ...over,
  };
}

const draw = (node: Node, over: Partial<Parameters<typeof MediaPhone>[0]> = {}) =>
  render(
    <MemoryRouter initialEntries={["/media"]}>
      <MediaPhone {...props(node, over)} />
      <Probe />
    </MemoryRouter>,
  );

let play: ReturnType<typeof vi.spyOn>;
let pause: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  resetTags();
  folders.set([]);
  hiddenFolders.set([]);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  resetTags();
});

describe("the phone line", () => {
  it("is the place, the artist and the tag title, on one line", () => {
    const n = file("song.mp3");
    seedTags(n.id, { title: "The Title", artist: "The Artist", album: "The Album", duration_ms: 60000 });
    draw(n, { position: 3 });
    const line = screen.getByTestId("media-phone-line");
    expect(line.textContent).toBe("3. The Artist - The Title");
  });

  it("leaves the artist and the dash out when the tags have none", () => {
    const n = file("song.mp3");
    seedTags(n.id, { title: "The Title" });
    draw(n, { position: 2 });
    expect(screen.getByTestId("media-phone-line").textContent).toBe("2. The Title");
  });

  it("falls back to the file name when there are no tags", () => {
    draw(file("plain.mp3"), { position: 1 });
    expect(screen.getByTestId("media-phone-line").textContent).toBe("1. plain.mp3");
  });

  it("leaves the artist out for a video, whose title is its name", () => {
    const n = file("clip.webm", { mime_type: "video/webm" });
    draw(n, { position: 4 });
    expect(screen.getByTestId("media-phone-line").textContent).toBe("4. clip.webm");
  });
});

describe("the phone transport", () => {
  it("plays and pauses the one element, as the bar does", () => {
    const el = document.createElement("audio");
    const n = file("song.mp3");
    const { rerender } = draw(n, { media: el, playing: false });
    fireEvent.click(screen.getByTestId("media-phone-play"));
    expect(play).toHaveBeenCalledTimes(1);
    expect(pause).not.toHaveBeenCalled();
    rerender(
      <MemoryRouter initialEntries={["/media"]}>
        <MediaPhone {...props(n, { media: el, playing: true })} />
        <Probe />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByTestId("media-phone-play"));
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it("says press Play when the browser refuses for want of a press", async () => {
    play.mockImplementation(() => Promise.reject(new DOMException("needs a gesture", "NotAllowedError")));
    const onPress = vi.fn();
    const el = document.createElement("audio");
    draw(file("song.mp3"), { media: el, onPress });
    fireEvent.click(screen.getByTestId("media-phone-play"));
    await waitFor(() => expect(onPress).toHaveBeenCalledWith(true));
  });

  it("steps with the same navigation the bar uses", () => {
    const nav = { next: vi.fn(), previous: vi.fn() };
    draw(file("song.mp3"), { nav });
    fireEvent.click(screen.getByTestId("media-phone-prev"));
    expect(nav.previous).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("media-phone-next"));
    expect(nav.next).toHaveBeenCalledTimes(1);
  });

  it("names each button as the bar does, for its tooltip", () => {
    draw(file("song.mp3"), { media: document.createElement("audio") });
    expect(screen.getByTestId("media-phone-prev").getAttribute("aria-label")).toBe("Previous file");
    expect(screen.getByTestId("media-phone-play").getAttribute("aria-label")).toBe("Play");
    expect(screen.getByTestId("media-phone-next").getAttribute("aria-label")).toBe("Next file");
  });
});

describe("the phone action row", () => {
  it("has Playlist, Download and More, each an icon over its label", () => {
    draw(file("song.mp3"));
    const row = screen.getByTestId("media-phone-actions");
    for (const name of ["Playlist", "Download", "More"]) {
      expect(within(row).getByRole("button", { name })).toBeTruthy();
    }
  });

  it("hides Download when the person may not download", () => {
    draw(file("song.mp3"), { onDownload: undefined });
    const row = screen.getByTestId("media-phone-actions");
    expect(within(row).queryByRole("button", { name: "Download" })).toBeNull();
    expect(within(row).getByRole("button", { name: "Playlist" })).toBeTruthy();
    expect(within(row).getByRole("button", { name: "More" })).toBeTruthy();
  });

  it("Playlist opens the owner's flow, Download downloads the file", () => {
    const onPlaylist = vi.fn();
    const onDownload = vi.fn();
    draw(file("song.mp3"), { onPlaylist, onDownload });
    fireEvent.click(screen.getByTestId("media-phone-playlist"));
    expect(onPlaylist).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("media-phone-download"));
    expect(onDownload).toHaveBeenCalledTimes(1);
  });

  it("More holds Speed, Shuffle, Repeat, Details and Open folder", async () => {
    draw(file("song.mp3"), { media: document.createElement("audio") });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    for (const name of ["Speed", "Shuffle", "Repeat off", "Details"]) {
      expect(await screen.findByRole("menuitem", { name })).toBeTruthy();
    }
    expect(await screen.findByRole("menuitem", { name: "Open folder" })).toBeTruthy();
  });

  it("Speed offers the bar's rates and sets the element's", async () => {
    const el = document.createElement("audio");
    draw(file("song.mp3"), { media: el });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Speed" }));
    const rate = await screen.findByRole("menuitemradio", { name: "1.5×" });
    fireEvent.click(rate);
    expect(el.playbackRate).toBe(1.5);
  });

  it("Shuffle and Repeat run the queue's own actions, and say their state", async () => {
    const onShuffle = vi.fn();
    const onRepeat = vi.fn();
    const n = file("song.mp3");
    const { rerender } = draw(n, { media: document.createElement("audio"), onShuffle, onRepeat, shuffle: true, repeat: "all" });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    const shuffle = await screen.findByRole("menuitemcheckbox", { name: "Shuffle" });
    expect(shuffle.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(shuffle);
    expect(onShuffle).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("media-phone-more"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Repeat all" }));
    expect(onRepeat).toHaveBeenCalledTimes(1);
    rerender(
      <MemoryRouter initialEntries={["/media"]}>
        <MediaPhone {...props(n, { media: document.createElement("audio"), onShuffle, onRepeat, repeat: "one" })} />
        <Probe />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByTestId("media-phone-more"));
    expect(await screen.findByRole("menuitem", { name: "Repeat one" })).toBeTruthy();
  });

  it("Details opens the owner's drawer", async () => {
    const onDetails = vi.fn();
    draw(file("song.mp3"), { media: document.createElement("audio"), onDetails });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Details" }));
    expect(onDetails).toHaveBeenCalledTimes(1);
  });

  it("Open folder goes to the folder the file sits in", async () => {
    draw(file("song.mp3"), { media: document.createElement("audio") });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Open folder" }));
    await waitFor(() => expect(screen.getByTestId("probe").textContent).toBe("/files/f1/d1"));
  });

  it("Open folder at a space's top goes to the space's own address", async () => {
    folders.set([{ id: "f1", name: "Team", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true, root_node_id: "d1" } as never]);
    draw(file("song.mp3"), { media: document.createElement("audio") });
    fireEvent.click(screen.getByTestId("media-phone-more"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Open folder" }));
    await waitFor(() => expect(screen.getByTestId("probe").textContent).toBe("/files/f1"));
  });
});