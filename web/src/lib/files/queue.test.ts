// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Node } from "./types";
import {
  MAX_FAILURE_RUN,
  MAX_QUEUE,
  addEnd,
  addNext,
  advance,
  appendPage,
  back,
  cycleRepeat,
  failed,
  move,
  newQueue,
  played,
  remove,
  restricted,
  toggleShuffle,
  type Queue,
} from "./queue";

// node makes a playable file of the given name. The id is the name, so the tests read in plain words.
function node(name: string, extra: Partial<Node> = {}): Node {
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

// seq is a generator that returns the numbers in order, so a shuffle is repeatable in a test.
function seq(values: number[]): () => number {
  let i = 0;
  return () => values[i++ % values.length];
}

// playAll takes steps until the queue stops, and returns the ids played in order. Each played file counts as played.
function playAll(q: Queue, more = false, limit = 100): string[] {
  const out: string[] = [];
  for (let i = 0; i < limit; i++) {
    const a = advance(q, more, () => 0.5);
    if (a.kind !== "play") break;
    q = played(a.queue, a.id);
    out.push(a.id);
  }
  return out;
}

describe("newQueue", () => {
  it("starts at the file given, and a no-download file starts ok like any other (playing is not downloading)", () => {
    const q = newQueue(
      [node("a.mp4"), node("b.mp4", { no_download: true, role: "viewer" }), node("c.mp3")],
      "c.mp3",
      { shuffle: false, repeat: "off" },
    );
    expect(q.current).toBe("c.mp3");
    expect(q.entries.map((e) => e.mark)).toEqual(["ok", "ok", "ok"]);
  });
});

describe("advance without shuffle", () => {
  it("plays a no-download file in the queue's order, as any other", () => {
    const q = newQueue(
      [node("1.mp4"), node("2.mp4", { no_download: true, role: "viewer" }), node("3.mp4"), node("4.mp4")],
      "1.mp4",
      { shuffle: false, repeat: "off" },
    );
    expect(playAll(q)).toEqual(["2.mp4", "3.mp4", "4.mp4"]);
  });

  it("skips a file the server refused a link for (restricted) without another request", () => {
    const q = restricted(newQueue([node("1.mp4"), node("2.mp4"), node("3.mp4")], "1.mp4", { shuffle: false, repeat: "off" }), "2.mp4");
    expect(playAll(q)).toEqual(["3.mp4"]);
  });

  it("stops at the end when repeat is off, and starts again from the top when repeat is all", () => {
    const base = newQueue([node("1.mp4"), node("2.mp4")], "2.mp4", { shuffle: false, repeat: "off" });
    const stop = advance(base, false, () => 0.5);
    expect(stop).toMatchObject({ kind: "stop", reason: "end" });
    const again = newQueue([node("1.mp4"), node("2.mp4")], "2.mp4", { shuffle: false, repeat: "all" });
    const wrapped = advance(again, false, () => 0.5);
    expect(wrapped).toMatchObject({ kind: "play", id: "1.mp4" });
  });

  it("asks for more of the folder before it decides the queue has ended", () => {
    const q = newQueue([node("1.mp4")], "1.mp4", { shuffle: false, repeat: "all" });
    expect(advance(q, true, () => 0.5)).toEqual({ kind: "more" });
    expect(advance(q, false, () => 0.5)).toMatchObject({ kind: "play", id: "1.mp4" });
  });

  it("goes back to the file before the current one in the queue's order", () => {
    const q = newQueue([node("1.mp4"), node("2.mp4"), node("3.mp4")], "3.mp4", { shuffle: false, repeat: "off" });
    expect(back(q)).toMatchObject({ id: "2.mp4" });
    expect(back(newQueue([node("1.mp4")], "1.mp4", { shuffle: false, repeat: "off" }))).toBeNull();
  });
});

describe("shuffle", () => {
  it("plays every file once before any file repeats, and then starts a new round with repeat all", () => {
    const names = ["a.mp4", "b.mp4", "c.mp4", "d.mp4", "e.mp4"];
    const start = newQueue(names.map((n) => node(n)), "a.mp4", { shuffle: false, repeat: "all" });
    const shuffled = toggleShuffle(start, seq([0.9, 0.1, 0.5, 0.3, 0.7]));
    let q = shuffled;
    const order: string[] = ["a.mp4"];
    for (let i = 0; i < 4; i++) {
      const a = advance(q, false, seq([0.2, 0.6, 0.4]));
      if (a.kind !== "play") throw new Error("the round stopped early");
      q = played(a.queue, a.id);
      order.push(a.id);
    }
    expect(new Set(order).size).toBe(5);
    const next = advance(q, false, seq([0.2, 0.6, 0.4]));
    expect(next).toMatchObject({ kind: "play" });
    expect(names).toContain(next.kind === "play" ? next.id : "");
  });

  it("does not play a file twice in a round when repeat is off, and stops after the last", () => {
    const q = toggleShuffle(newQueue([node("a.mp4"), node("b.mp4"), node("c.mp4")], "a.mp4", { shuffle: false, repeat: "off" }), () => 0.5);
    const played3 = playAll(q);
    expect(played3.sort()).toEqual(["b.mp4", "c.mp4"]);
  });

  it("takes in files added to the queue while it is shuffled", () => {
    const q = toggleShuffle(newQueue([node("a.mp4")], "a.mp4", { shuffle: false, repeat: "off" }), () => 0.5);
    const more = appendPage(q, [node("z.mp4")], () => 0.5).queue;
    expect(playAll(more)).toEqual(["z.mp4"]);
  });

  it("goes back through the history while it is shuffled", () => {
    let q = toggleShuffle(newQueue([node("a.mp4"), node("b.mp4"), node("c.mp4")], "a.mp4", { shuffle: false, repeat: "off" }), seq([0.1, 0.9]));
    const first = advance(q, false, () => 0.5);
    if (first.kind !== "play") throw new Error("expected a file");
    q = played(first.queue, first.id);
    expect(back(q)).toMatchObject({ id: "a.mp4" });
  });
});

describe("repeat", () => {
  it("cycles off, all, one, off", () => {
    let q = newQueue([node("a.mp4")], "a.mp4", { shuffle: false, repeat: "off" });
    const modes: string[] = [];
    for (let i = 0; i < 4; i++) {
      q = cycleRepeat(q);
      modes.push(q.repeat);
    }
    expect(modes).toEqual(["all", "one", "off", "all"]);
  });
});

describe("failures", () => {
  it("skips a file marked as failed, and never plays it again in the queue", () => {
    const q = failed(newQueue([node("1.mp4"), node("2.mp4"), node("3.mp4")], "1.mp4", { shuffle: false, repeat: "off" }), "2.mp4");
    expect(playAll(q)).toEqual(["3.mp4"]);
    expect(q.entries[1].mark).toBe("failed");
  });

  it("stops with the none reason, nothing could be played, when every file fails", () => {
    let q = newQueue([node("1.mp4"), node("2.mp4")], "1.mp4", { shuffle: false, repeat: "all" });
    q = failed(q, "1.mp4");
    q = failed(q, "2.mp4");
    expect(advance(q, false, () => 0.5)).toMatchObject({ kind: "stop", reason: "none" });
  });

  it("stops after a run of failures even when other files are left, so no endless loop", () => {
    // The budget is ten files in a row, written out here: the test does not read the constant it is checking.
    const nodes = Array.from({ length: 15 }, (_, i) => node(`${String(i).padStart(2, "0")}.mp4`));
    let q = newQueue(nodes, nodes[0].id, { shuffle: false, repeat: "off" });
    let attempts = 0;
    for (;;) {
      const a = advance(q, false, () => 0.5);
      if (a.kind !== "play") {
        expect(a).toMatchObject({ kind: "stop", reason: "failed" });
        break;
      }
      attempts++;
      q = failed(a.queue, a.id);
    }
    expect(attempts).toBe(10);
    expect(MAX_FAILURE_RUN).toBe(10);
  });

  it("starts the run again after a file plays", () => {
    const nodes = [node("1.mp4"), node("2.mp4"), node("3.mp4")];
    let q = failed(newQueue(nodes, "1.mp4", { shuffle: false, repeat: "off" }), "1.mp4");
    expect(q.run).toBe(1);
    const a = advance(q, false, () => 0.5);
    if (a.kind !== "play") throw new Error("expected 2.mp4 to play");
    q = played(a.queue, a.id);
    expect(q.run).toBe(0);
  });

  it("marks a restricted file, and a restricted file is skipped without a request", () => {
    const q = restricted(newQueue([node("1.mp4"), node("2.mp4")], "1.mp4", { shuffle: false, repeat: "off" }), "2.mp4");
    expect(q.entries[1].mark).toBe("restricted");
    expect(advance(q, false, () => 0.5)).toMatchObject({ kind: "stop", reason: "end" });
  });
});

describe("editing the queue", () => {
  it("adds a file after the current one, and at the end, and never twice", () => {
    const q = newQueue([node("1.mp4"), node("2.mp4")], "1.mp4", { shuffle: false, repeat: "off" });
    expect(addNext(q, node("3.mp4"), () => 0.5).entries.map((e) => e.node.id)).toEqual(["1.mp4", "3.mp4", "2.mp4"]);
    expect(addEnd(q, node("3.mp4")).entries.map((e) => e.node.id)).toEqual(["1.mp4", "2.mp4", "3.mp4"]);
    expect(addEnd(q, node("1.mp4")).entries).toHaveLength(2);
  });

  it("removes a file and moves a file up or down, with the bounds kept", () => {
    const q = newQueue([node("1.mp4"), node("2.mp4"), node("3.mp4")], "1.mp4", { shuffle: false, repeat: "off" });
    expect(move(q, "3.mp4", -1).entries.map((e) => e.node.id)).toEqual(["1.mp4", "3.mp4", "2.mp4"]);
    expect(move(q, "1.mp4", -1).entries.map((e) => e.node.id)).toEqual(["1.mp4", "2.mp4", "3.mp4"]);
    expect(remove(q, "2.mp4").entries.map((e) => e.node.id)).toEqual(["1.mp4", "3.mp4"]);
  });

  it("plays in the new order after a move: Previous and Next follow it", () => {
    // 3 moves to the top: the queue is 3, 2, 1, and the file on screen is 2.
    const q = move(newQueue([node("1.mp4"), node("2.mp4"), node("3.mp4")], "2.mp4", { shuffle: false, repeat: "off" }), "3.mp4", -2);
    expect(q.entries.map((e) => e.node.id)).toEqual(["3.mp4", "2.mp4", "1.mp4"]);
    expect(playAll(q)).toEqual(["1.mp4"]);
    expect(back(q)).toMatchObject({ id: "3.mp4" });
  });

  it("keeps the queue at most MAX_QUEUE long and says when it has been cut", () => {
    const first = Array.from({ length: MAX_QUEUE - 1 }, (_, i) => node(`f${i}.mp4`));
    const q = newQueue(first, "f0.mp4", { shuffle: false, repeat: "off" });
    const { queue, capped } = appendPage(q, [node("x.mp4"), node("y.mp4")], () => 0.5);
    expect(queue.entries).toHaveLength(MAX_QUEUE);
    expect(capped).toBe(true);
  });

  it("stops at the cap without a request for more, and says so only once it is reached", () => {
    const q = newQueue([node("1.mp4")], "1.mp4", { shuffle: false, repeat: "off" });
    expect(appendPage(q, [node("2.mp4")], () => 0.5).capped).toBe(false);
  });

  it("removing the current file leaves no current, so the caller plays the next one", () => {
    const q = remove(newQueue([node("1.mp4"), node("2.mp4")], "1.mp4", { shuffle: false, repeat: "off" }), "1.mp4");
    expect(q.current).toBeNull();
    expect(advance(q, false, () => 0.5)).toMatchObject({ kind: "play", id: "2.mp4" });
  });
});
