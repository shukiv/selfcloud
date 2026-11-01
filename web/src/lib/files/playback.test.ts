// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "./types";
import { Pager } from "./pager";
import { closeViewer, getPlayback, setCurrent, setMedia, showViewer, startPlayback, stopPlayback } from "./playback";
import { session, type Session } from "../session";

const node = (name: string, mime: string) => ({ id: `n-${name}`, name, kind: "file", mime_type: mime, size: 1 }) as unknown as Node;
const seedOf = (n: Node) => ({ start: n, folder: new Pager([n], null, async () => ({ items: [] })) });
const audio = node("a.ogg", "audio/ogg");
const video = node("v.mp4", "video/mp4");
const el = () => document.createElement("audio");

beforeEach(() => {
  stopPlayback();
});
afterEach(() => {
  stopPlayback();
  session.set(null);
});

describe("the session's viewer and the mini player", () => {
  it("starts a session with the viewer open, and a new id for each start", () => {
    startPlayback(seedOf(audio));
    const first = getPlayback().id;
    expect(getPlayback().viewerOpen).toBe(true);
    startPlayback(seedOf(video));
    expect(getPlayback().id).toBe(first + 1);
  });

  it("keeps audio with a player when its viewer closes, and tells the page once", () => {
    const told = vi.fn();
    startPlayback(seedOf(audio), told);
    setCurrent({ node: audio, kind: "audio" });
    setMedia(el());
    closeViewer();
    expect(getPlayback().seed).not.toBeNull();
    expect(getPlayback().viewerOpen).toBe(false);
    expect(told).toHaveBeenCalledTimes(1);
    expect(told).toHaveBeenCalledWith(audio);
    closeViewer();
    expect(told).toHaveBeenCalledTimes(1);
  });

  it("ends the session when the viewer closes on a video, and tells the page", () => {
    const told = vi.fn();
    startPlayback(seedOf(video), told);
    setCurrent({ node: video, kind: "video" });
    setMedia(document.createElement("video"));
    closeViewer();
    expect(getPlayback().seed).toBeNull();
    expect(told).toHaveBeenCalledWith(video);
  });

  it("ends the session when audio closes before its player exists", () => {
    startPlayback(seedOf(audio));
    setCurrent({ node: audio, kind: "audio" });
    closeViewer();
    expect(getPlayback().seed).toBeNull();
  });

  it("ends the session at once on a stop, and drops the queue with it", () => {
    startPlayback(seedOf(audio));
    setCurrent({ node: audio, kind: "audio" });
    setMedia(el());
    stopPlayback();
    expect(getPlayback()).toMatchObject({ seed: null, current: null, media: null, nav: null });
  });

  it("opens the viewer again on a session that is kept", () => {
    startPlayback(seedOf(audio));
    setCurrent({ node: audio, kind: "audio" });
    setMedia(el());
    closeViewer();
    showViewer();
    expect(getPlayback().viewerOpen).toBe(true);
  });

  it("ignores a player that reports itself when no session runs", () => {
    setMedia(el());
    setCurrent({ node: audio, kind: "audio" });
    expect(getPlayback()).toMatchObject({ seed: null, media: null, current: null });
  });
});

describe("a change of person ends the session", () => {
  const who = (id: string): Session => ({ user: { id, email: `${id}@x.test` }, account: null, csrf: `c-${id}`, publicLinks: false }) as unknown as Session;

  it("stops the playback, and clears the queue, when another person signs in", () => {
    session.set(who("usr_a"));
    startPlayback(seedOf(audio));
    setCurrent({ node: audio, kind: "audio" });
    setMedia(el());
    session.set(who("usr_b"));
    expect(getPlayback()).toMatchObject({ seed: null, current: null, media: null });
  });

  it("stops the playback on a sign-out", () => {
    session.set(who("usr_a"));
    startPlayback(seedOf(audio));
    session.set(null);
    expect(getPlayback().seed).toBeNull();
  });

  it("does not stop a session for the same person's first value", () => {
    session.set(who("usr_a"));
    startPlayback(seedOf(audio));
    session.set(who("usr_a"));
    expect(getPlayback().seed).not.toBeNull();
  });
});
