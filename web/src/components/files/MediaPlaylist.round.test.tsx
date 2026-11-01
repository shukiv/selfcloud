// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist's second review round: a pick or a Next made while a page is being read wins over the step that waits for
// it; a link the player cannot renew is asked of the share; shuffle pressed twice, or left by the viewer, reads no more;
// a page that cannot be read says so and can be read again; and the add panel of a selection adds from the folder.

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import { Pager, type PageLike } from "../../lib/files/pager";
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
    no_download: false,
    role: "viewer",
    editors_can_share: false,
  } as unknown as Node;
}

const linkTo = (node: Node) => ({ url: `https://content.example/${node.id}` });

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

const playing = (container: HTMLElement) => container.querySelector("video, audio")?.getAttribute("src") ?? null;

// deferred is a promise the test answers when it chooses.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("the playlist's steps and picks", () => {
  it("a pick made while the next page is read wins: the step that waits does nothing once the page comes", async () => {
    const a = file("a.webm");
    const x = file("x.webm");
    const page = deferred<PageLike>();
    const fetchPage = vi.fn(() => page.promise);
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={x} folder={new Pager([a, x], "c1", fetchPage)} fetchLink={fetchLink} />);
    await flush();
    // x ends: the playlist has nothing loaded after it, so it reads the next page and waits.
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    // The person picks a while the page is on its way.
    fireEvent.click(screen.getByRole("button", { name: "a.webm" }));
    await flush();
    expect(playing(container)).toBe("https://content.example/a.webm");
    await act(async () => {
      page.resolve({ items: [file("b.webm")] });
    });
    await flush();
    // The pick stands: a plays, and the step that was waiting does not move on past it.
    expect(playing(container)).toBe("https://content.example/a.webm");
  });

  it("two Next presses while a page is read move one file, the second one's", async () => {
    const a = file("a.webm");
    const x = file("x.webm");
    const page = deferred<PageLike>();
    const fetchPage = vi.fn(() => page.promise);
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={x} folder={new Pager([a, x], "c1", fetchPage)} fetchLink={fetchLink} />);
    await flush();
    fireEvent.keyDown(window, { key: "n" });
    fireEvent.keyDown(window, { key: "n" });
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    await act(async () => {
      page.resolve({ items: [file("b.webm"), file("c.webm")] });
    });
    await flush();
    // Nothing is loaded after x, so the page's first file is the one after it: b, not c (the first press is superseded).
    expect(playing(container)).toBe("https://content.example/b.webm");
  });
});

describe("a link the player cannot renew", () => {
  // renewalRefused plays the first link and refuses the renewal with the given status.
  function renewalRefused(status: number, onLost: (err: unknown) => Promise<boolean>) {
    const a = file("a.webm");
    const b = file("b.webm");
    // The first link of a is given; its renewal (the second ask for a) is refused. Other files are not refused.
    const fetchLink = vi.fn(async (n: Node) => {
      if (n.id === "a.webm" && fetchLink.mock.calls.filter((c) => c[0].id === "a.webm").length > 1) throw new ApiError(status, "unauthorized", {}, 0);
      return linkTo(n);
    });
    const { container } = render(
      <MediaPlaylist start={a} folder={new Pager([a, b], null, async () => ({ items: [] }))} fetchLink={fetchLink} onLost={onLost} />,
    );
    return { container, fetchLink };
  }

  it("is the share's answer: when the share is gone the page's lost view shows, and the playlist stops", async () => {
    const onLost = vi.fn(async (_err: unknown) => true);
    const { container } = renewalRefused(401, onLost);
    await flush();
    const video = container.querySelector("video") as HTMLVideoElement;
    Object.defineProperty(video, "error", { value: { code: 4 }, configurable: true });
    fireEvent.error(video);
    await vi.waitFor(() => expect(screen.getByText("This share is no longer available.")).toBeTruthy());
    expect(onLost).toHaveBeenCalledTimes(1);
    expect((onLost.mock.calls[0][0] as ApiError).status).toBe(401);
    // The playlist did not move on to b.
    expect(screen.queryByText(/can't play/)).toBeNull();
  });

  it("is that one file's failure when the share is still there: the playlist moves on to the next file", async () => {
    const onLost = vi.fn(async (_err: unknown) => false);
    const { container } = renewalRefused(404, onLost);
    await flush();
    const video = container.querySelector("video") as HTMLVideoElement;
    Object.defineProperty(video, "error", { value: { code: 4 }, configurable: true });
    fireEvent.error(video);
    await vi.waitFor(() => expect(screen.getByText("This file can't play. Skipping it.")).toBeTruthy());
    expect(onLost).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(playing(container)).toBe("https://content.example/b.webm"), { timeout: 4000 });
  });
});

describe("shuffle and the viewer's close", () => {
  it("a second press while the folder is read does nothing, so shuffle ends on", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const page = deferred<PageLike>();
    const fetchPage = vi.fn(() => page.promise);
    render(<MediaPlaylist start={a} folder={new Pager([a], "c1", fetchPage)} fetchLink={async (n) => linkTo(n)} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    await act(async () => {
      page.resolve({ items: [b], next_cursor: undefined });
    });
    await flush();
    expect(screen.getByRole("button", { name: "Shuffle" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("reads no further page once the viewer has closed it", async () => {
    const a = file("a.webm");
    const page = deferred<PageLike>();
    // The first read is held until the viewer has closed; a read after that answers the folder's end at once, so a loop
    // that keeps reading ends here and the count shows it.
    let reads = 0;
    const fetchPage = vi.fn(() => {
      reads++;
      return reads === 1 ? page.promise : Promise.resolve({ items: [], next_cursor: undefined });
    });
    const { unmount } = render(<MediaPlaylist start={a} folder={new Pager([a], "c1", fetchPage)} fetchLink={async (n) => linkTo(n)} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    unmount();
    // The page comes after the close and has a next page: the loop must not ask for it.
    await act(async () => {
      page.resolve({ items: [file("b.webm")], next_cursor: "c2" });
    });
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });
});

describe("a folder page that cannot be read", () => {
  it("says the folder could not be read (not that the playlist ended), and reads the page again when asked", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    let fail = true;
    const fetchPage = vi.fn(async (): Promise<PageLike> => {
      if (fail) throw new Error("offline");
      return { items: [b] };
    });
    const fetchLink = vi.fn(async (n: Node) => linkTo(n));
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a], "c1", fetchPage)} fetchLink={fetchLink} />);
    await flush();
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    const screenNow = screen.getByTestId("media-screen");
    expect(within(screenNow).getByText("The folder could not be read past this point, so the list may have more files. Try again.")).toBeTruthy();
    expect(screen.queryByText("The playlist has ended.")).toBeNull();
    // The retry reads the same page, and b plays.
    fail = false;
    fireEvent.click(within(screenNow).getByRole("button", { name: "Try again" }));
    await vi.waitFor(() => expect(playing(container)).toBe("https://content.example/b.webm"));
    expect(fetchPage).toHaveBeenNthCalledWith(2, "c1");
  });

  it("the panel's own alert, after Load more fails, offers the read again", async () => {
    const a = file("a.webm");
    let fail = true;
    const fetchPage = vi.fn(async (): Promise<PageLike> => {
      if (fail) throw new Error("offline");
      return { items: [file("b.webm")] };
    });
    render(<MediaPlaylist start={a} folder={new Pager([a], "c1", fetchPage)} fetchLink={async (n) => linkTo(n)} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    fireEvent.click(screen.getByRole("button", { name: "Load more files" }));
    await flush();
    const alert = screen.getByTestId("media-playlist").querySelector('.ant-alert[role="alert"]') as HTMLElement;
    expect(alert.textContent).toContain("could not be read");
    fail = false;
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));
    await flush();
    expect(fetchPage).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "b.webm" })).toBeTruthy();
  });

  it("shows one message, not two, when the playlist stops unreadable after the panel's Load more failed too", async () => {
    const a = file("a.webm");
    const fetchPage = vi.fn(async (): Promise<PageLike> => {
      throw new Error("offline");
    });
    const { container } = render(<MediaPlaylist start={a} folder={new Pager([a], "c1", fetchPage)} fetchLink={async (n) => linkTo(n)} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    fireEvent.click(screen.getByRole("button", { name: "Load more files" }));
    await flush();
    // The file ends with no next file: the playlist reads the page again, fails, and stops as unreadable.
    fireEvent.ended(container.querySelector("video") as HTMLVideoElement);
    await flush();
    expect(screen.getAllByText("The folder could not be read past this point, so the list may have more files. Try again.")).toHaveLength(1);
  });
});

describe("the add panel of a selection", () => {
  it("offers the folder's files that are not in the selection", async () => {
    const a = file("a.webm");
    const b = file("b.webm");
    const c = file("c.webm");
    render(<MediaPlaylist start={a} folder={new Pager([a, b, c], null, async () => ({ items: [] }))} selection={[a]} fetchLink={async (n) => linkTo(n)} />);
    await flush();
    fireEvent.click(screen.getByRole("button", { name: "Add files" }));
    const panel = screen.getByTestId("media-add");
    expect(within(panel).getByText("b.webm")).toBeTruthy();
    expect(within(panel).getByText("c.webm")).toBeTruthy();
    expect(within(panel).queryByText("a.webm")).toBeNull();
  });
});
