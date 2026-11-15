// SPDX-License-Identifier: AGPL-3.0-or-later

// The sleep timer's store: a timed choice counts across the queue's files and pauses whatever plays when it ends,
// End of this file only marks the end, and a session that ends (a stop, or a change of person) turns the timer Off.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "./types";
import { Pager } from "./pager";
import { getPlayback, setMedia, startPlayback, stopPlayback } from "./playback";
import { getSleep, setSleep, stopsAtEnd, subscribeSleep } from "./sleepTimer";
import { session, type Session } from "../session";

const node = (name: string) => ({ id: `n-${name}`, name, kind: "file", mime_type: "audio/ogg", size: 1 }) as unknown as Node;
const seedOf = (n: Node) => ({ start: n, folder: new Pager([n], null, async () => ({ items: [] })) });
const audio = node("a.ogg");
const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;

beforeEach(() => {
  vi.useFakeTimers();
  stopPlayback();
  setSleep("off");
});

afterEach(() => {
  setSleep("off");
  stopPlayback();
  session.set(null);
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("a timed choice", () => {
  it("pauses the element that plays when the time is up, and turns Off", () => {
    startPlayback(seedOf(audio));
    const el = document.createElement("audio");
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {});
    setMedia(el);
    setSleep(15);
    expect(getSleep().choice).toBe(15);
    expect(getSleep().endsAt).toBe(Date.now() + 15 * 60_000);
    vi.advanceTimersByTime(14 * 60_000);
    expect(pause).not.toHaveBeenCalled();
    expect(getSleep().choice).toBe(15);
    vi.advanceTimersByTime(60_000);
    expect(pause).toHaveBeenCalledTimes(1);
    expect(getSleep().choice).toBe("off");
    expect(getSleep().endsAt).toBeNull();
    // The element is only paused: a later timeupdate does not pause it again.
    vi.advanceTimersByTime(60 * 60_000);
    expect(pause).toHaveBeenCalledTimes(1);
  });

  it("keeps counting when the queue moves to a new element, and pauses the new one", () => {
    startPlayback(seedOf(audio));
    const first = document.createElement("audio");
    const second = document.createElement("audio");
    const pauseFirst = vi.spyOn(first, "pause").mockImplementation(() => {});
    const pauseSecond = vi.spyOn(second, "pause").mockImplementation(() => {});
    setMedia(first);
    setSleep(15);
    vi.advanceTimersByTime(10 * 60_000);
    // The queue moves on: a new file plays in a new element while the choice counts.
    setMedia(second);
    expect(getPlayback().media).toBe(second);
    vi.advanceTimersByTime(5 * 60_000);
    expect(pauseFirst).not.toHaveBeenCalled();
    expect(pauseSecond).toHaveBeenCalledTimes(1);
    expect(getSleep().choice).toBe("off");
  });

  it("replaces the choice running with a new count of its own", () => {
    startPlayback(seedOf(audio));
    const el = document.createElement("audio");
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {});
    setMedia(el);
    setSleep(15);
    vi.advanceTimersByTime(10 * 60_000);
    setSleep(30);
    // The first choice's end comes and goes without a pause.
    vi.advanceTimersByTime(5 * 60_000);
    expect(pause).not.toHaveBeenCalled();
    vi.advanceTimersByTime(25 * 60_000);
    expect(pause).toHaveBeenCalledTimes(1);
    expect(getSleep().choice).toBe("off");
  });

  it("pauses nothing when no element plays at the end, and still turns Off", () => {
    setSleep(15);
    vi.advanceTimersByTime(15 * 60_000);
    expect(getSleep().choice).toBe("off");
  });
});

describe("End of this file", () => {
  it("makes stopsAtEnd true once, with nothing timed, until the choice is cleared", () => {
    expect(stopsAtEnd()).toBe(false);
    setSleep("end");
    expect(stopsAtEnd()).toBe(true);
    expect(getSleep()).toEqual({ choice: "end", endsAt: null });
    startPlayback(seedOf(audio));
    const el = document.createElement("audio");
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {});
    setMedia(el);
    vi.advanceTimersByTime(60 * 60_000);
    expect(pause).not.toHaveBeenCalled();
    expect(stopsAtEnd()).toBe(true);
    setSleep("off");
    expect(stopsAtEnd()).toBe(false);
  });

  it("gives way to a timed choice", () => {
    setSleep("end");
    setSleep(15);
    expect(stopsAtEnd()).toBe(false);
    expect(getSleep().choice).toBe(15);
  });
});

describe("a session that ends", () => {
  it("turns the timer Off on a stop, and the end pauses nothing", () => {
    startPlayback(seedOf(audio));
    const el = document.createElement("audio");
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {});
    setMedia(el);
    setSleep(30);
    stopPlayback();
    expect(getSleep().choice).toBe("off");
    vi.advanceTimersByTime(30 * 60_000);
    expect(pause).not.toHaveBeenCalled();
  });

  it("turns the timer Off when the person changes, so another person's player is never paused", () => {
    session.set(who("usr_a"));
    startPlayback(seedOf(audio));
    const el = document.createElement("audio");
    const pause = vi.spyOn(el, "pause").mockImplementation(() => {});
    setMedia(el);
    setSleep(45);
    session.set(who("usr_b"));
    expect(getPlayback().seed).toBeNull();
    expect(getSleep().choice).toBe("off");
    // The next person's player is left alone when the old count would have ended.
    startPlayback(seedOf(audio));
    const next = document.createElement("audio");
    const pauseNext = vi.spyOn(next, "pause").mockImplementation(() => {});
    setMedia(next);
    vi.advanceTimersByTime(45 * 60_000);
    expect(pause).not.toHaveBeenCalled();
    expect(pauseNext).not.toHaveBeenCalled();
  });

  it("turns the timer Off on a sign-out", () => {
    session.set(who("usr_a"));
    startPlayback(seedOf(audio));
    setSleep(60);
    session.set(null);
    expect(getSleep().choice).toBe("off");
  });
});

describe("subscribeSleep", () => {
  it("tells of every change until the listener leaves", () => {
    const fn = vi.fn();
    const leave = subscribeSleep(fn);
    setSleep(15);
    expect(fn).toHaveBeenCalledTimes(1);
    setSleep("off");
    expect(fn).toHaveBeenCalledTimes(2);
    leave();
    setSleep(30);
    expect(fn).toHaveBeenCalledTimes(2);
  });
});
