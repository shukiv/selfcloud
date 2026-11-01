// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist's races and shuffle: a link answer that arrives after the person has left its file, a file that ends at the
// moment the person picks another, and shuffle that reads every page of the folder before it deals the round.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Pager } from "../../lib/files/pager";
import type { Node } from "../../lib/files/types";
import MediaPlaylist from "./MediaPlaylist";

function file(name: string): Node {
  return {
    id: name,
    folder_id: "f",
    parent_id: "d",
    name,
    kind: "file",
    mime_type: "video/webm",
    size: 1,
    created_at: "",
    updated_at: "",
  } as unknown as Node;
}

const linkTo = (node: Node) => ({ url: `https://content.example/${node.id}` });

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

const playing = (container: HTMLElement) => container.querySelector("video, audio")?.getAttribute("src") ?? null;

describe("the playlist's races and shuffle", () => {
  it("ignores the link answer of a file the person has left for another", async () => {
    const files = [file("a.webm"), file("b.webm")];
    let resolveA: (v: { url: string }) => void = () => {};
    const fetchLink = vi.fn((n: Node) => {
      if (n.id === "a.webm") return new Promise<{ url: string }>((r) => (resolveA = r));
      return Promise.resolve(linkTo(n));
    });
    const { container } = render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // a's link is still on its way; the person picks b.
    fireEvent.click(screen.getByRole("button", { name: "b.webm" }));
    await flush();
    expect(playing(container)).toBe("https://content.example/b.webm");
    // a's answer arrives late: it must not replace b.
    await act(async () => {
      resolveA(linkTo(files[0]));
    });
    await flush();
    expect(playing(container)).toBe("https://content.example/b.webm");
  });

  it("a file ending at the same moment the person picks one plays the one picked", async () => {
    const files = [file("a.webm"), file("b.webm"), file("c.webm")];
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    const video = container.querySelector("video") as HTMLVideoElement;
    fireEvent.ended(video);
    fireEvent.click(screen.getByRole("button", { name: "c.webm" }));
    await flush();
    await flush();
    expect(playing(container)).toBe("https://content.example/c.webm");
  });

  it("with shuffle on, reads every page of the folder up to the cap first, so the round covers them all", async () => {
    const first = [file("a.webm"), file("b.webm")];
    const later = [file("c.webm"), file("d.webm")];
    const folder = new Pager(first, "p2", async () => ({ items: later, next_cursor: undefined }));
    render(<MediaPlaylist start={first[0]} folder={folder} fetchLink={vi.fn(async (n: Node) => linkTo(n))} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    await flush();
    await flush();
    expect(screen.getAllByTestId("media-row")).toHaveLength(4);
    expect(folder.hasMore).toBe(false);
  });
});
