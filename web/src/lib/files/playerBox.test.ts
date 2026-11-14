// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parkBox, placeBox, playerBox } from "./playerBox";

// The box is one div for the whole page: the session's player lives in it, and it moves between the viewer, the Media
// page's stage and the hidden parking place without its element being made again. These tests move it between targets
// of their own, and park it between cases.

type Mover = HTMLElement & { moveBefore?: (node: Node, ref: Node | null) => void };

let target: HTMLDivElement;

beforeEach(() => {
  target = document.createElement("div");
  document.body.appendChild(target);
});

afterEach(() => {
  playerBox().replaceChildren();
  parkBox();
  target.remove();
  vi.restoreAllMocks();
});

describe("playerBox", () => {
  it("is one div, a flex column with the session's gap, kept across calls", () => {
    const box = playerBox();
    expect(playerBox()).toBe(box);
    expect(box.style.display).toBe("flex");
    expect(box.style.flexDirection).toBe("column");
    expect(box.style.gap).toBe("12px");
    expect(box.style.flex).toBe("1 1 auto");
    expect(box.style.minHeight).toBe("0px");
  });
});

describe("placeBox", () => {
  it("moves the box into the target, keeping what is in it", () => {
    const box = playerBox();
    const audio = document.createElement("audio");
    box.appendChild(audio);
    placeBox(target);
    expect(box.parentElement).toBe(target);
    expect(box.querySelector("audio")).toBe(audio);
  });

  it("does nothing when the box already sits in the target", () => {
    placeBox(target);
    const append = vi.spyOn(target, "appendChild");
    placeBox(target);
    expect(append).not.toHaveBeenCalled();
  });

  it("resumes a file that played before the move and was paused by it", () => {
    const box = playerBox();
    const audio = document.createElement("audio");
    box.appendChild(audio);
    let paused = false;
    Object.defineProperty(audio, "paused", { get: () => paused, configurable: true });
    Object.defineProperty(audio, "ended", { value: false, configurable: true });
    const play = vi.fn(() => {
      paused = false;
      return Promise.resolve();
    });
    audio.play = play;
    // A move that pauses the file, as a re-insertion may: the box's own resume starts it again.
    const append = target.appendChild.bind(target);
    target.appendChild = ((node: Node) => {
      const r = append(node);
      if (node === box) paused = true;
      return r;
    }) as typeof target.appendChild;
    placeBox(target);
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("does not start a file that was paused before the move", () => {
    const box = playerBox();
    const audio = document.createElement("audio");
    box.appendChild(audio);
    Object.defineProperty(audio, "paused", { value: true, configurable: true });
    const play = vi.fn(() => Promise.resolve());
    audio.play = play;
    placeBox(target);
    expect(play).not.toHaveBeenCalled();
  });

  it("does not start a file that had ended before the move", () => {
    const box = playerBox();
    const audio = document.createElement("audio");
    box.appendChild(audio);
    Object.defineProperty(audio, "paused", { value: false, configurable: true });
    Object.defineProperty(audio, "ended", { value: true, configurable: true });
    const play = vi.fn(() => Promise.resolve());
    audio.play = play;
    placeBox(target);
    expect(play).not.toHaveBeenCalled();
  });

  it("swallows a resume the browser refuses", async () => {
    const box = playerBox();
    const audio = document.createElement("audio");
    box.appendChild(audio);
    let paused = false;
    Object.defineProperty(audio, "paused", { get: () => paused, configurable: true });
    Object.defineProperty(audio, "ended", { value: false, configurable: true });
    const play = vi.fn(() => {
      paused = false;
      return Promise.reject(new DOMException("needs a gesture", "NotAllowedError"));
    });
    audio.play = play;
    const append = target.appendChild.bind(target);
    target.appendChild = ((node: Node) => {
      const r = append(node);
      if (node === box) paused = true;
      return r;
    }) as typeof target.appendChild;
    placeBox(target);
    await Promise.resolve();
    await Promise.resolve();
    expect(play).toHaveBeenCalledTimes(1);
  });

  it("uses moveBefore when the target has it and both ends are in the page", () => {
    const box = playerBox();
    document.body.appendChild(box);
    const moveBefore = vi.fn((node: Node, ref: Node | null) => {
      target.insertBefore(node, ref);
      return node;
    });
    (target as Mover).moveBefore = moveBefore;
    const append = vi.spyOn(target, "appendChild");
    placeBox(target);
    expect(moveBefore).toHaveBeenCalledWith(box, null);
    expect(append).not.toHaveBeenCalled();
    expect(box.parentElement).toBe(target);
  });

  it("falls back to appendChild when moveBefore throws", () => {
    const box = playerBox();
    document.body.appendChild(box);
    (target as Mover).moveBefore = () => {
      throw new Error("the move was refused");
    };
    placeBox(target);
    expect(box.parentElement).toBe(target);
  });

  it("does not use moveBefore while the box is out of the page", () => {
    const box = playerBox();
    box.remove();
    const moveBefore = vi.fn((node: Node, ref: Node | null) => {
      target.insertBefore(node, ref);
      return node;
    });
    (target as Mover).moveBefore = moveBefore;
    placeBox(target);
    expect(moveBefore).not.toHaveBeenCalled();
    expect(box.parentElement).toBe(target);
  });
});

describe("parkBox", () => {
  it("keeps the box in one hidden place on the body, out of the way until the next placement", () => {
    placeBox(target);
    parkBox();
    const box = playerBox();
    const parking = box.parentElement as HTMLElement;
    expect(parking).not.toBe(target);
    expect(parking.style.display).toBe("none");
    expect(parking.parentElement).toBe(document.body);
    expect(document.body.contains(box)).toBe(true);
    parkBox();
    expect(box.parentElement).toBe(parking);
    placeBox(target);
    expect(box.parentElement).toBe(target);
  });
});
