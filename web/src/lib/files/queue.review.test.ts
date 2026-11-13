// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's rules: a file that is the only one left plays again under repeat, a file the person
// picks starts the failure run again, a file picked in a shuffled round leaves the round, and the queue holds at most
// MAX_QUEUE files and says when it has left some out.

import { describe, expect, it } from "vitest";
import type { Node } from "./types";
import { MAX_FAILURE_RUN, MAX_QUEUE, addEnd, addNext, advance, appendPage, back, choose, failed, newQueue, restricted, type Queue } from "./queue";
// The helpers above are the queue's own; the tests read the answers it gives.

function node(name: string): Node {
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

const names = (n: number, prefix = "t") => Array.from({ length: n }, (_, i) => node(`${prefix}-${String(i).padStart(4, "0")}.ogg`));
const never = () => 0.5;

describe("repeat all when one file is left", () => {
  it("plays the same file again, and says so, when it is the only playable one", () => {
    const q = newQueue([node("a.ogg")], "a.ogg", { shuffle: false, repeat: "all" });
    const a = advance(q, false, never);
    expect(a.kind).toBe("play");
    if (a.kind !== "play") return;
    expect(a.id).toBe("a.ogg");
    expect(a.again).toBe(true);
  });

  it("plays the same file again when every other file has failed", () => {
    let q = newQueue([node("a.ogg"), node("b.ogg"), node("c.ogg")], "a.ogg", { shuffle: false, repeat: "all" });
    q = failed(q, "b.ogg");
    q = failed(q, "c.ogg");
    const a = advance(q, false, never);
    expect(a.kind).toBe("play");
    if (a.kind !== "play") return;
    expect(a.id).toBe("a.ogg");
    expect(a.again).toBe(true);
  });

  it("does not say again when a different file plays", () => {
    const q = newQueue([node("a.ogg"), node("b.ogg")], "a.ogg", { shuffle: false, repeat: "all" });
    const a = advance(q, false, never);
    expect(a.kind === "play" && a.again).toBe(false);
  });

  it("shuffled, with one file left, plays it again", () => {
    const q = newQueue([node("a.ogg")], "a.ogg", { shuffle: true, repeat: "all" }, never);
    const a = advance(q, false, never);
    expect(a.kind === "play" && a.again).toBe(true);
  });
});

describe("a file the person picks", () => {
  it("starts the failure run again, so a stopped playlist can carry on", () => {
    let q = newQueue(names(MAX_FAILURE_RUN + 2), names(1)[0].id, { shuffle: false, repeat: "off" });
    for (let i = 0; i < MAX_FAILURE_RUN; i++) q = failed(q, q.entries[i + 1].node.id);
    expect(q.run).toBe(MAX_FAILURE_RUN);
    const picked = choose(q, q.entries[MAX_FAILURE_RUN + 1].node.id);
    expect(picked.run).toBe(0);
    expect(picked.current).toBe(q.entries[MAX_FAILURE_RUN + 1].node.id);
  });

  it("does not pick a file the person may not download, and picks a failed file again to retry it", () => {
    let q = newQueue([node("a.ogg"), node("b.ogg"), node("c.ogg")], "a.ogg", { shuffle: false, repeat: "off" });
    q = restricted(q, "b.ogg");
    expect(choose(q, "b.ogg")).toBe(q);
    q = failed(q, "c.ogg");
    const retried = choose(q, "c.ogg");
    expect(retried.current).toBe("c.ogg");
    expect(retried.entries.find((e) => e.node.id === "c.ogg")?.mark).toBe("ok");
  });

  it("under shuffle leaves the round: it does not play again in the same round", () => {
    const files = names(4);
    let q = newQueue(files, files[0].id, { shuffle: true, repeat: "off" }, never);
    const picked = files[3].id;
    q = choose(q, picked);
    expect(q.plan).not.toContain(picked);
    // Play the rest of the round: the picked file never comes up again in it.
    const seen: string[] = [];
    for (let i = 0; i < 10; i++) {
      const a = advance(q, false, never);
      if (a.kind !== "play") break;
      seen.push(a.id);
      q = a.queue;
    }
    expect(seen).not.toContain(picked);
  });

  it("under shuffle, Previous goes back to the file that was on screen before the pick, not past it", () => {
    const files = names(4);
    let q = newQueue(files, files[0].id, { shuffle: true, repeat: "off" }, never);
    const before = q.current as string;
    q = choose(q, files[3].id);
    const b = back(q);
    expect(b?.id).toBe(before);
  });
});

describe("the queue holds MAX_QUEUE files", () => {
  it("keeps the first MAX_QUEUE playable files of a longer list and says it has been cut", () => {
    const q = newQueue(names(MAX_QUEUE + 20), names(1)[0].id, { shuffle: false, repeat: "off" });
    expect(q.entries.length).toBe(MAX_QUEUE);
    expect(q.capped).toBe(true);
  });

  it("does not say it is cut at exactly MAX_QUEUE files", () => {
    const q = newQueue(names(MAX_QUEUE), names(1)[0].id, { shuffle: false, repeat: "off" });
    expect(q.entries.length).toBe(MAX_QUEUE);
    expect(q.capped).toBe(false);
  });

  it("addNext on a full queue adds nothing and says it is cut", () => {
    const full = newQueue(names(MAX_QUEUE), names(1)[0].id, { shuffle: false, repeat: "off" });
    const q = addNext({ ...full, capped: false }, node("extra.ogg"));
    expect(q.entries.length).toBe(MAX_QUEUE);
    expect(q.capped).toBe(true);
  });

  it("addEnd on a full queue adds nothing and says it is cut", () => {
    const full: Queue = { ...newQueue(names(MAX_QUEUE), names(1)[0].id, { shuffle: false, repeat: "off" }), capped: false };
    const q = addEnd(full, node("extra.ogg"));
    expect(q.entries.length).toBe(MAX_QUEUE);
    expect(q.capped).toBe(true);
  });

  it("appendPage takes only what room is left, and says it cut the rest", () => {
    const base = newQueue(names(MAX_QUEUE - 5), names(1)[0].id, { shuffle: false, repeat: "off" });
    const r = appendPage(base, names(10, "u"));
    expect(r.queue.entries.length).toBe(MAX_QUEUE);
    expect(r.capped).toBe(true);
  });
});

describe("the failure stop", () => {
  it("stops with reason failed once the run is spent, and only a pick starts again", () => {
    let q = newQueue(names(MAX_FAILURE_RUN + 3), names(1)[0].id, { shuffle: false, repeat: "off" });
    for (let i = 1; i <= MAX_FAILURE_RUN; i++) q = failed(q, q.entries[i].node.id);
    const a = advance(q, false, never);
    expect(a.kind === "stop" && a.reason).toBe("failed");
  });
});
