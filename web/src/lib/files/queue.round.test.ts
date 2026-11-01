// SPDX-License-Identifier: AGPL-3.0-or-later

// The queue's second review round: a page added to a shuffled queue does not change the round the queue already holds.

import { describe, expect, it } from "vitest";
import type { Node } from "./types";
import { appendPage, newQueue } from "./queue";

function node(name: string): Node {
  return {
    id: name,
    folder_id: "f",
    parent_id: "d",
    name,
    kind: "file",
    size: 1,
    mime_type: "video/mp4",
    created_at: "2026-10-31T00:00:00Z",
    modified_at: "2026-10-31T00:00:00Z",
    no_download: false,
    editors_can_share: false,
  } as unknown as Node;
}

describe("appendPage", () => {
  it("adds the page's files to the round without changing the round it was given", () => {
    const q = newQueue([node("a.mp4"), node("b.mp4"), node("c.mp4")], "a.mp4", { shuffle: true, repeat: "off" }, () => 0.3);
    const roundBefore = [...q.plan];
    expect(roundBefore.length).toBe(2);
    const r = appendPage(q, [node("d.mp4"), node("e.mp4")], () => 0.5);
    // The given queue's round is the same list it was: the page went into a copy.
    expect(q.plan).toEqual(roundBefore);
    expect(r.queue.plan.length).toBe(roundBefore.length + 2);
    expect([...r.queue.plan].sort()).toEqual(["b.mp4", "c.mp4", "d.mp4", "e.mp4"]);
  });
});
