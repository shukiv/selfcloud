// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
    size: 1,
    mime_type: name.endsWith(".mp3") ? "audio/mpeg" : "video/webm",
    created_at: "2026-10-31T00:00:00Z",
    modified_at: "2026-10-31T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
  };
}

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

// mount renders the playlist of two videos, with a spy on the keys that reach the page above it, as the viewer's own key handler is.
function mount(active = true, start = file("a.webm")) {
  const keys = vi.fn();
  const element = (
    <div onKeyDown={keys}>
      <MediaPlaylist
        start={start}
        folder={new Pager([start, file("b.webm")], null, async () => ({ items: [] }))}
        fetchLink={async (n) => ({ url: `https://content.example/${n.id}` })}
        active={active}
      />
    </div>
  );
  const view = render(element);
  return { ...view, keys, element };
}

const toggle = () => screen.getByRole("button", { name: "Playlist" });
const close = () => screen.getByRole("button", { name: "Close playlist" });
const drawer = (container: HTMLElement) => container.querySelector("[data-media-drawer]") as HTMLElement;

describe("MediaPlaylist drawer (decision 63)", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("is closed at the start, and its toggle says so", async () => {
    const { container } = mount();
    await flush();
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
    expect(drawer(container).classList.contains("media-drawer--closed")).toBe(true);
    expect(drawer(container).dataset.open).toBe("false");
  });

  it("opens by the toggle, and the focus moves into the drawer", async () => {
    const { container } = mount();
    await flush();
    fireEvent.click(toggle());
    expect(toggle().getAttribute("aria-expanded")).toBe("true");
    expect(drawer(container).classList.contains("media-drawer--closed")).toBe(false);
    // The focus moves one task after the open, once the drawer is shown (MediaPlaylist).
    await waitFor(() => expect(document.activeElement).toBe(close()));
  });

  it("closes by its own X, and the focus goes back to the toggle", async () => {
    const { container } = mount();
    await flush();
    fireEvent.click(toggle());
    fireEvent.click(close());
    expect(drawer(container).dataset.open).toBe("false");
    expect(document.activeElement).toBe(toggle());
  });

  it("closes by the toggle as well", async () => {
    const { container } = mount();
    await flush();
    fireEvent.click(toggle());
    fireEvent.click(toggle());
    expect(drawer(container).dataset.open).toBe("false");
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
  });

  it("Escape closes the drawer and not the viewer: the page above never sees that key", async () => {
    const { container, keys } = mount();
    await flush();
    fireEvent.click(toggle());
    fireEvent.keyDown(close(), { key: "Escape" });
    expect(drawer(container).dataset.open).toBe("false");
    expect(keys).not.toHaveBeenCalled();
  });

  it("Escape with the drawer closed reaches the viewer, as before", async () => {
    const { keys } = mount();
    await flush();
    fireEvent.keyDown(toggle(), { key: "Escape" });
    // The page's own key handler (the viewer's) is given the Escape: the drawer did not take it.
    expect(keys).toHaveBeenCalledTimes(1);
    expect(keys.mock.calls[0][0].key).toBe("Escape");
  });

  it("a closed drawer is inert: none of its controls can take the focus, so Tab skips it", async () => {
    const { container } = mount();
    await flush();
    const d = drawer(container);
    expect(d.closest("[inert]")).not.toBeNull();
    for (const b of Array.from(d.querySelectorAll("button"))) {
      expect(b.closest("[inert]")).not.toBeNull();
    }
    // The focus goes to the toggle in the viewer's head, and a closed drawer does not take it.
    toggle().focus();
    expect(document.activeElement).toBe(toggle());
  });

  it("Escape with a queue row's menu open closes the menu only: the drawer stays open", async () => {
    const { container } = mount();
    await flush();
    fireEvent.click(toggle());
    await waitFor(() => expect(document.activeElement).toBe(close()));
    // The row's menu is an antd Dropdown: its menu is drawn outside the drawer, and its focus is in the menu.
    const more = screen.getAllByRole("button", { name: /More actions for/ })[0];
    fireEvent.click(more);
    const menu = await waitFor(() => {
      const m = document.querySelector('[role="menu"]');
      if (!m) throw new Error("no menu yet");
      return m as HTMLElement;
    });
    const item = menu.querySelector('[role="menuitem"]') as HTMLElement;
    item.focus();
    fireEvent.keyDown(item, { key: "Escape" });
    expect(drawer(container).dataset.open).toBe("true");
  });

  it("is closed again on every opening of the viewer, and its open state is not kept", async () => {
    const a = file("a.webm");
    const view = (active: boolean) => (
      <MediaPlaylist start={a} folder={new Pager([a], null, async () => ({ items: [] }))} fetchLink={async (n) => ({ url: n.id })} active={active} />
    );
    const { container, rerender } = render(view(true));
    await flush();
    fireEvent.click(toggle());
    expect(drawer(container).dataset.open).toBe("true");
    // The viewer closes (active off), then opens again: the drawer is closed.
    rerender(view(false));
    await flush();
    rerender(view(true));
    await flush();
    expect(drawer(container).dataset.open).toBe("false");
    expect(toggle().getAttribute("aria-expanded")).toBe("false");
  });

  it("keeps the queue mounted while the drawer is closed, so its rows stay", async () => {
    const { container } = mount();
    await flush();
    expect(container.querySelector('[data-testid="media-queue"]')).not.toBeNull();
    fireEvent.click(toggle());
    fireEvent.click(close());
    expect(container.querySelectorAll('[data-testid="media-row"]').length).toBe(2);
  });

  it("a one-file playlist still has the toggle, since Add files lives in the drawer", async () => {
    const a = file("a.webm");
    render(<MediaPlaylist start={a} folder={new Pager([a], null, async () => ({ items: [] }))} fetchLink={async (n) => ({ url: n.id })} />);
    await flush();
    expect(toggle()).toBeTruthy();
  });
});

// The Media page (mp1) draws the queue in a slot of its own, below the player: no drawer and no Playlist toggle, and the
// player keeps its place in the tree, so moving the queue out of the drawer never makes the element again.
describe("MediaPlaylist with the queue in a page slot", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  function mountSlot(slot: HTMLElement | null) {
    const start = file("a.webm");
    const view = render(
      <MediaPlaylist
        start={start}
        folder={new Pager([start, file("b.webm")], null, async () => ({ items: [] }))}
        fetchLink={async (n) => ({ url: `https://content.example/${n.id}` })}
        active
        queueSlot={slot}
      />,
    );
    return view;
  }

  it("draws the queue as a table in the slot, with no drawer, no toggle and no drawer rows", async () => {
    const slot = document.createElement("div");
    document.body.appendChild(slot);
    const { container } = mountSlot(slot);
    await flush();
    expect(screen.queryByRole("button", { name: "Playlist" })).toBeNull();
    expect(container.querySelector("[data-media-drawer]")).toBeNull();
    // The table is lazy (Listing stays out of the drawer's load), so it arrives a tick after the slot is given.
    await waitFor(() => expect(slot.querySelector('[data-testid="media-queue-table"]')).not.toBeNull());
    expect(slot.querySelectorAll('[data-testid="media-qrow"]').length).toBe(2);
    expect(slot.querySelector('[data-testid="media-row"]')).toBeNull();
    expect(container.querySelector('[data-testid="media-screen"] video')).not.toBeNull();
    slot.remove();
  });

  it("a null slot draws no queue at all, and the player still plays", async () => {
    const { container } = mountSlot(null);
    await flush();
    expect(screen.queryByRole("button", { name: "Playlist" })).toBeNull();
    expect(container.querySelector("[data-media-drawer]")).toBeNull();
    expect(document.querySelector('[data-testid="media-queue"]')).toBeNull();
    expect(container.querySelector("video")).not.toBeNull();
  });

  it("moving the queue from the drawer to a slot keeps the same element, never reporting it gone", async () => {
    const start = file("a.webm");
    const onElement = vi.fn();
    const props = {
      start,
      folder: new Pager([start, file("b.webm")], null, async () => ({ items: [] })),
      fetchLink: async (n: Node) => ({ url: `https://content.example/${n.id}` }),
      active: true,
      onElement,
    };
    const { rerender, container } = render(<MediaPlaylist {...props} />);
    await flush();
    // The player reports null while it mounts (its element state settles a tick after the ref); the move is what must
    // never report it gone. The count is taken once the element has been reported, and the moves are judged from there.
    await waitFor(() => expect(onElement.mock.calls.some(([v]) => v !== null)).toBe(true));
    const video = container.querySelector("video") as HTMLVideoElement;
    expect(video).not.toBeNull();
    const first = onElement.mock.calls.findIndex(([v]) => v !== null);
    expect(onElement.mock.calls[first][0]).toBe(video);
    const from = onElement.mock.calls.length;
    const slot = document.createElement("div");
    document.body.appendChild(slot);
    rerender(<MediaPlaylist {...props} queueSlot={slot} />);
    await flush();
    expect(container.querySelector("video")).toBe(video);
    await waitFor(() => expect(slot.querySelector('[data-testid="media-queue-table"]')).not.toBeNull());
    expect(container.querySelector("[data-media-drawer]")).toBeNull();
    // And back: the drawer returns with the queue in it, and the player is still the same element.
    rerender(<MediaPlaylist {...props} />);
    await flush();
    expect(container.querySelector("video")).toBe(video);
    expect(container.querySelector("[data-media-drawer]")).not.toBeNull();
    expect(container.querySelector('[data-testid="media-queue"]')).not.toBeNull();
    const later = onElement.mock.calls.slice(from);
    expect(later.some(([v]) => v === null)).toBe(false);
    expect(later.filter(([v]) => v !== null).every(([v]) => v === video)).toBe(true);
    slot.remove();
  });
});
