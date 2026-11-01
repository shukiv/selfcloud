// SPDX-License-Identifier: AGPL-3.0-or-later

// The current row says what the player does: "Playing" only while the file plays, and a paused row says "Paused".

import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Entry } from "../../lib/files/queue";
import type { Node } from "../../lib/files/types";
import MediaQueue from "./MediaQueue";

function node(name: string): Node {
  return { id: name, folder_id: "f", parent_id: "d", name, kind: "file", mime_type: "audio/ogg", size: 1, created_at: "", updated_at: "" } as unknown as Node;
}

function render1(playing: boolean | undefined) {
  const entries: Entry[] = [{ node: node("a.ogg"), mark: "ok" }];
  return render(
    <MediaQueue
      entries={entries}
      currentId="a.ogg"
      repeat="off"
      shuffle={false}
      capped={false}
      moreLeft={false}
      busy={false}
      candidates={[]}
      adding={false}
      playing={playing}
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

describe("MediaQueue current row state", () => {
  it("says Playing only while the file plays", () => {
    const { container } = render1(true);
    expect(container.querySelector(".media-state")?.textContent).toContain("Playing");
  });

  it("says Paused, not Playing, on a current row whose file is paused or has not started", () => {
    const { container } = render1(false);
    const text = container.querySelector(".media-state")?.textContent ?? "";
    expect(text).not.toContain("Playing");
    expect(text).toContain("Paused");
  });
});
