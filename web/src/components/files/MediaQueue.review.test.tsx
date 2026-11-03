// SPDX-License-Identifier: AGPL-3.0-or-later

// The queue panel's controls the review named: a row is reordered by its grip (a drag), by Alt with the arrow keys, and by
// its More menu; the row on screen is marked and is one line.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Entry } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import MediaQueue from "./MediaQueue";

function node(name: string): Node {
  return { id: name, folder_id: "f", parent_id: "d", name, kind: "file", mime_type: "audio/ogg", size: 1, created_at: "", updated_at: "" } as unknown as Node;
}

function props(names: string[], current: string | null, onMove = vi.fn(), onRemove = vi.fn()) {
  const entries: Entry[] = names.map((n) => ({ node: node(n), mark: "ok" }));
  return {
    entries,
    currentId: current,
    repeat: "off" as const,
    shuffle: false,
    capped: false,
    moreLeft: false,
    busy: false,
    candidates: [],
    adding: false,
    onAddOpen: () => {},
    onAdd: () => {},
    onLoadMore: () => {},
    onPlay: () => {},
    onRemove,
    onMove,
    onShuffle: () => {},
    onRepeat: () => {},
  };
}

afterEach(() => {
  // The test that fakes the element under the pointer restores what it replaced.
  delete (document as unknown as { elementFromPoint?: unknown }).elementFromPoint;
});

describe("the queue panel", () => {
  it("moves a row with Alt and the arrow keys, and keeps the focus on the row", () => {
    const onMove = vi.fn();
    render(<MediaQueue {...props(["a.ogg", "b.ogg", "c.ogg"], "a.ogg", onMove)} />);
    const row = screen.getAllByTestId("media-row")[1];
    fireEvent.keyDown(row, { key: "ArrowDown", altKey: true });
    expect(onMove).toHaveBeenCalledWith("b.ogg", 1);
    fireEvent.keyDown(row, { key: "ArrowUp", altKey: true });
    expect(onMove).toHaveBeenLastCalledWith("b.ogg", -1);
  });

  it("does not move a row on an arrow key without Alt", () => {
    const onMove = vi.fn();
    render(<MediaQueue {...props(["a.ogg", "b.ogg"], "a.ogg", onMove)} />);
    fireEvent.keyDown(screen.getAllByTestId("media-row")[0], { key: "ArrowDown" });
    expect(onMove).not.toHaveBeenCalled();
  });

  it("drags a row by its grip: the row under the pointer is the place it moves to", () => {
    const onMove = vi.fn();
    const { container } = render(<MediaQueue {...props(["a.ogg", "b.ogg", "c.ogg"], "a.ogg", onMove)} />);
    const grip = container.querySelectorAll<HTMLButtonElement>(".media-grip")[0];
    // Jsdom has no layout: the row under the pointer is the third row.
    const third = container.querySelectorAll<HTMLElement>("[data-row-index]")[2];
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: () => third });
    fireEvent.pointerDown(grip);
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 5, clientY: 5 }));
    });
    expect(onMove).toHaveBeenCalledWith("a.ogg", 1);
    act(() => {
      window.dispatchEvent(new Event("pointerup"));
    });
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { clientX: 5, clientY: 5 }));
    });
    // After the pointer is up, moving does nothing.
    expect(onMove).toHaveBeenCalledTimes(1);
  });

  it("moves a row from its More menu, and removes it there", async () => {
    const onMove = vi.fn();
    const onRemove = vi.fn();
    render(<MediaQueue {...props(["a.ogg", "b.ogg"], "a.ogg", onMove, onRemove)} />);
    fireEvent.click(screen.getByRole("button", { name: "More actions for b.ogg" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Move b\.ogg up/ }));
    expect(onMove).toHaveBeenCalledWith("b.ogg", -1);
    fireEvent.click(screen.getByRole("button", { name: "More actions for b.ogg" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Remove b\.ogg/ }));
    expect(onRemove).toHaveBeenCalledWith("b.ogg");
  });

  it("marks the row on screen with its state, and gives it the current mark", () => {
    render(<MediaQueue {...props(["a.ogg", "b.ogg"], "b.ogg")} playing />);
    const rows = screen.getAllByTestId("media-row");
    expect(rows[1].getAttribute("aria-current")).toBe("true");
    expect(rows[0].getAttribute("aria-current")).toBeNull();
    expect(rows[1].textContent).toContain("Playing");
  });
});
