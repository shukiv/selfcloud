// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist drawer's Shuffle, Repeat and Add files are icons with names (ic2, decision 72): no visible words, their pressed
// states say what is on, and Repeat shows the repeat-one icon in that mode.
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Entry } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import MediaQueue from "./MediaQueue";

function node(name: string): Node {
  return { id: name, folder_id: "f", parent_id: "d", name, kind: "file", mime_type: "audio/ogg", size: 1, created_at: "", updated_at: "" } as unknown as Node;
}

function renderQueue(repeat: "off" | "all" | "one", shuffle: boolean) {
  const entries: Entry[] = [{ node: node("a.ogg"), mark: "ok" }];
  return render(
    <MediaQueue
      entries={entries}
      currentId="a.ogg"
      repeat={repeat}
      shuffle={shuffle}
      capped={false}
      moreLeft={false}
      busy={false}
      candidates={[]}
      adding={false}
      playing={false}
      onAddOpen={() => {}}
      onAdd={() => {}}
      onLoadMore={() => {}}
      onPlay={vi.fn()}
      onRemove={() => {}}
      onMove={() => {}}
      onShuffle={() => {}}
      onRepeat={() => {}}
    />,
  );
}

describe("MediaQueue toolbar icons (ic2, decision 72)", () => {
  it("Shuffle is an icon named Shuffle, pressed when shuffle is on", () => {
    renderQueue("off", true);
    const b = screen.getByRole("button", { name: "Shuffle" });
    expect(b.textContent).toBe("");
    expect(b.getAttribute("aria-pressed")).toBe("true");
  });

  it("Shuffle is not pressed when shuffle is off", () => {
    renderQueue("off", false);
    expect(screen.getByRole("button", { name: "Shuffle" }).getAttribute("aria-pressed")).toBe("false");
  });

  it("Repeat is an icon named for its state, pressed unless it is off", () => {
    renderQueue("all", false);
    const b = screen.getByRole("button", { name: "Repeat: Repeat all" });
    expect(b.textContent).toBe("");
    expect(b.getAttribute("aria-pressed")).toBe("true");
  });

  it("Repeat is not pressed when it is off", () => {
    renderQueue("off", false);
    expect(screen.getByRole("button", { name: "Repeat: Repeat off" }).getAttribute("aria-pressed")).toBe("false");
  });

  it("Add files is an icon named Add files", () => {
    renderQueue("off", false);
    const b = screen.getByRole("button", { name: "Add files" });
    expect(b.textContent).toBe("");
  });
});
