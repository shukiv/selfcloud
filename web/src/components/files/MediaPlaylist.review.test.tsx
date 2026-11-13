// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist's rules: a pick clears a stop and starts the failure run again (the player mounts), a
// pending skip never moves past a file the person chose, a file that is the only one left plays again under repeat all,
// and a file's 404 is that file's failure, not the share's end.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import { Pager } from "../../lib/files/pager";
import type { Node } from "../../lib/files/types";
import MediaPlaylist, { SKIP_MS } from "./MediaPlaylist";

function file(name: string): Node {
  return {
    id: name,
    folder_id: "f",
    parent_id: "d",
    name,
    kind: "file",
    mime_type: name.endsWith(".ogg") ? "audio/ogg" : "video/webm",
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

async function pass(ms: number): Promise<void> {
  await act(async () => {
    vi.advanceTimersByTime(ms);
  });
  await flush();
}

const playing = (container: HTMLElement) => container.querySelector("video, audio")?.getAttribute("src") ?? null;

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("a pick after the playlist stopped", () => {
  it("clears the stop and mounts the player for the file the person picks", async () => {
    // Every file is refused except the last: the run of failures stops nothing (only three files), so the stop is "none"
    // until a file is picked. Here the list is all refused, so it stops with nothing to play; then the person picks b.
    const files = [file("a.webm"), file("b.webm")];
    let ok = false;
    const fetchLink = vi.fn(async (n: Node) => {
      if (ok && n.id === "b.webm") return linkTo(n);
      throw new ApiError(500, "internal", {}, 0);
    });
    const { container } = render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // a and b each fail and pass their pause; then nothing is left to play.
    await pass(SKIP_MS + 10);
    await pass(SKIP_MS + 10);
    await pass(SKIP_MS + 10);
    expect(screen.getByRole("status").textContent).toContain("could be played");
    ok = true;
    fireEvent.click(screen.getByRole("button", { name: "b.webm" }));
    await flush();
    await flush();
    // The stop message leaves with its exit animation; the player is what must be there.
    await waitFor(() => expect(screen.queryByText(/could be played/)).toBeNull());
    expect(playing(container)).toBe("https://content.example/b.webm");
  });

  it("after the failure run stops, a pick plays again and the run starts from zero", async () => {
    const files = Array.from({ length: 13 }, (_, i) => file(`${String(i).padStart(2, "0")}.webm`));
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "12.webm") return linkTo(n);
      throw new ApiError(500, "internal", {}, 0);
    });
    render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // Ten refusals in a row stop the playlist with the carry-on offer.
    for (let i = 0; i < 10; i++) await pass(SKIP_MS + 10);
    expect(screen.getByRole("button", { name: "Carry on" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "12.webm" }));
    await flush();
    await flush();
    expect(screen.queryByRole("button", { name: "Carry on" })).toBeNull();
    expect(fetchLink.mock.calls.at(-1)?.[0].id).toBe("12.webm");
  });
});

describe("a pending skip", () => {
  it("does not move past a file the person chose during the pause", async () => {
    const files = [file("a.webm"), file("b.webm"), file("c.webm")];
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "a.webm") throw new ApiError(500, "internal", {}, 0);
      return linkTo(n);
    });
    render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    // a failed; the person picks c at once, inside the pause.
    fireEvent.click(screen.getByRole("button", { name: "c.webm" }));
    await flush();
    await pass(SKIP_MS + 10);
    await pass(SKIP_MS + 10);
    // The old timer did not move on from c: c is the one asked for last, and b was never asked for.
    expect(fetchLink.mock.calls.map((c) => c[0].id)).toEqual(["a.webm", "c.webm"]);
  });

  it("is cancelled by Next, so the file after the chosen one is not skipped past", async () => {
    const files = [file("a.webm"), file("b.webm"), file("c.webm")];
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "a.webm") throw new ApiError(500, "internal", {}, 0);
      return linkTo(n);
    });
    const { container } = render(<MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    await pass(1000);
    fireEvent.click(screen.getByRole("button", { name: "Next file" }));
    await flush();
    await pass(SKIP_MS + 10);
    expect(playing(container)).toBe("https://content.example/b.webm");
    expect(fetchLink.mock.calls.map((c) => c[0].id)).toEqual(["a.webm", "b.webm"]);
  });
});

describe("repeat all with one file left", () => {
  it("starts the same file again from the beginning, asking for its link again", async () => {
    const a = file("a.webm");
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a], null, async () => ({ items: [] }))} fetchLink={fetchLink} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: /^Repeat/ }));
    await flush();
    const before = fetchLink.mock.calls.length;
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    await flush();
    expect(fetchLink.mock.calls.length).toBe(before + 1);
    expect(playing(container)).toBe("https://content.example/a.webm");
  });
});

describe("a file's refused link", () => {
  it("is that file's failure when the share is still there: the playlist moves on", async () => {
    const files = [file("a.webm"), file("b.webm")];
    const onLost = vi.fn(async () => false);
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "a.webm") throw new ApiError(404, "not_found", {}, 0);
      return linkTo(n);
    });
    const { container } = render(
      <MediaPlaylist start={files[0]} folder={new Pager(files, null, async () => ({ items: [] }))} fetchLink={fetchLink} onLost={onLost} />,
    );
    await flush();
    await pass(SKIP_MS + 10);
    await flush();
    expect(onLost).toHaveBeenCalledTimes(1);
    expect(playing(container)).toBe("https://content.example/b.webm");
    expect(screen.queryByText(/no longer available/)).toBeNull();
  });
});
