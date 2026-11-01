// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { CUE_LINE_COARSE, CUE_LINE_FINE, decodeSubtitle, MAX_CUES, MAX_SUBTITLE_BYTES, parseCues, placeCue, readTracks } from "./subtitles";

describe("parseCues reads WebVTT and SRT alike", () => {
  it("reads a WebVTT file: the header is skipped and each cue keeps its text", () => {
    const cues = parseCues("WEBVTT\n\nNOTE a note\n\n00:00:01.000 --> 00:00:02.500\nHello\n\n00:01:00.000 --> 00:01:02.000 align:start\nTwo\nlines\n");
    expect(cues).toEqual([
      { start: 1, end: 2.5, text: "Hello" },
      { start: 60, end: 62, text: "Two\nlines" },
    ]);
  });

  it("reads an SRT file, whose timings use a comma and whose numbers are skipped", () => {
    const cues = parseCues("1\n00:00:01,000 --> 00:00:03,250\nFirst\n\n2\n01:02:03,004 --> 01:02:04,000\nSecond\n");
    expect(cues).toEqual([
      { start: 1, end: 3.25, text: "First" },
      { start: 3723.004, end: 3724, text: "Second" },
    ]);
  });

  it("reads CRLF line ends and a byte-order mark", () => {
    const cues = parseCues("﻿1\r\n00:00:00,000 --> 00:00:01,000\r\nHi\r\n");
    expect(cues).toEqual([{ start: 0, end: 1, text: "Hi" }]);
  });

  it("drops a cue whose end is not after its start", () => {
    expect(parseCues("00:00:02.000 --> 00:00:02.000\nSame\n\n00:00:03.000 --> 00:00:01.000\nBack\n\n00:00:04.000 --> 00:00:05.000\nOk\n")).toEqual([
      { start: 4, end: 5, text: "Ok" },
    ]);
  });

  it("drops a timing whose minutes or seconds are 60 or more", () => {
    expect(parseCues("00:60:00.000 --> 00:61:00.000\nNo\n\n00:00:00.000 --> 00:00:61.000\nNo\n")).toEqual([]);
  });

  it("keeps the markup of a cue as the file gave it, with no script", () => {
    expect(parseCues("00:00:00.000 --> 00:00:01.000\n<i>Hi</i> <script>x()</script>\n")[0].text).toBe("<i>Hi</i> <script>x()</script>");
  });

  it("stops at MAX_CUES, so a long file cannot fill the player", () => {
    const block = (i: number) => `00:00:${String(i % 60).padStart(2, "0")}.000 --> 00:00:${String(i % 60).padStart(2, "0")}.500\nx`;
    const many = Array.from({ length: MAX_CUES + 50 }, (_, i) => block(i)).join("\n\n");
    expect(parseCues(many)).toHaveLength(MAX_CUES);
  });
});

describe("readTracks reads each subtitle file beside a video", () => {
  it("gives one track per readable file, in the order given", async () => {
    const open = vi.fn(async (id: string) => new Response(`00:00:00.000 --> 00:00:01.000\n${id}`, { status: 200 }));
    const tracks = await readTracks(
      [
        { id: "a", name: "clip.vtt" },
        { id: "b", name: "clip.srt" },
      ],
      open,
    );
    expect(tracks.map((t) => t.label)).toEqual(["clip.vtt", "clip.srt"]);
    expect(tracks[1].cues[0].text).toBe("b");
  });

  it("asks for at most MAX_SUBTITLE_BYTES, as a range", async () => {
    const open = vi.fn(async () => new Response("", { status: 200 }));
    await readTracks([{ id: "a", name: "clip.vtt" }], open);
    expect(open).toHaveBeenCalledWith("a", `bytes=0-${MAX_SUBTITLE_BYTES - 1}`);
  });

  it("gives no track for a file the server refuses, and still gives the others", async () => {
    const open = vi.fn(async (id: string) => (id === "refused" ? new Response("no", { status: 403 }) : new Response("00:00:00.000 --> 00:00:01.000\nYes", { status: 200 })));
    const tracks = await readTracks(
      [
        { id: "refused", name: "a.vtt" },
        { id: "ok", name: "b.vtt" },
      ],
      open,
    );
    expect(tracks.map((t) => t.label)).toEqual(["b.vtt"]);
  });

  it("gives no track for a file whose read throws", async () => {
    const open = vi.fn(async () => {
      throw new TypeError("network");
    });
    expect(await readTracks([{ id: "a", name: "a.vtt" }], open)).toEqual([]);
  });
});

// The bytes of one cue, in the given charset: a Hebrew, Arabic or Russian word in the legacy code page of its script.
const cue = (text: Uint8Array) => {
  const head = new TextEncoder().encode("1\n00:00:01,000 --> 00:00:02,000\n");
  return new Uint8Array([...head, ...text, 0x0a]);
};

describe("decodeSubtitle reads the encoding a file is in", () => {
  it("reads Windows-1255 Hebrew when the interface is Hebrew, and not as garbage", () => {
    // "שלום" in Windows-1255.
    const bytes = cue(new Uint8Array([0xf9, 0xec, 0xe5, 0xed]));
    expect(decodeSubtitle(bytes, "he")).toContain("שלום");
  });

  it("reads Windows-1256 Arabic when the interface is Arabic", () => {
    // "ابت" in Windows-1256.
    expect(decodeSubtitle(cue(new Uint8Array([0xc7, 0xc8, 0xca])), "ar")).toContain("ابت");
  });

  it("reads Windows-1251 Russian when the interface is Russian", () => {
    // "Привет" in Windows-1251.
    expect(decodeSubtitle(cue(new Uint8Array([0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2])), "ru")).toContain("Привет");
  });

  it("reads valid UTF-8 as UTF-8, whatever the interface language", () => {
    const bytes = new TextEncoder().encode("שלום");
    expect(decodeSubtitle(bytes, "ru")).toBe("שלום");
  });

  it("honours a UTF-8 byte-order mark and drops it", () => {
    const bytes = new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode("hi")]);
    expect(decodeSubtitle(bytes, "en")).toBe("hi");
  });

  it("honours a UTF-16 byte-order mark in either order", () => {
    const le = new Uint8Array([0xff, 0xfe, 0x68, 0x00, 0x69, 0x00]);
    const be = new Uint8Array([0xfe, 0xff, 0x00, 0x68, 0x00, 0x69]);
    expect(decodeSubtitle(le, "en")).toBe("hi");
    expect(decodeSubtitle(be, "en")).toBe("hi");
  });

  it("drops a character cut at the end of a truncated file, rather than reading it as garbage", () => {
    // "a" then the first byte of "é" (C3 A9): cut after C3, as a range read of a long file can be.
    const cut = new Uint8Array([0x61, 0xc3]);
    expect(decodeSubtitle(cut, "en", true)).toBe("a");
    expect(decodeSubtitle(new Uint8Array([0x61, 0xc3, 0xa9]), "en", true)).toBe("a\u00e9");
  });

  it("reads a legacy file through readTracks in the interface language, and parses its cues", async () => {
    const bytes = new Uint8Array([
      ...new TextEncoder().encode("1\n00:00:01,000 --> 00:00:02,000\n"),
      0xf9, 0xec, 0xe5, 0xed, 0x0a,
    ]);
    const tracks = await readTracks([{ id: "a", name: "clip.srt" }], async () => new Response(bytes, { status: 200 }), "he");
    expect(tracks[0].cues).toEqual([{ start: 1, end: 2, text: "שלום" }]);
  });
});

describe("placeCue keeps a cue above the player's bar", () => {
  it("puts the bottom of a cue on a line of the picture, not on the bottom edge, on a pointer", () => {
    const cue = { snapToLines: true, line: -1, lineAlign: "start" } as { snapToLines: boolean; line: number; lineAlign: string };
    placeCue(cue as unknown as VTTCue, false);
    expect(cue.snapToLines).toBe(false);
    expect(cue.line).toBe(CUE_LINE_FINE);
    expect(cue.lineAlign).toBe("end");
  });

  it("places a cue higher on a coarse pointer, where the phone's bar is taller", () => {
    const cue = { snapToLines: true, line: -1, lineAlign: "start" } as { snapToLines: boolean; line: number; lineAlign: string };
    placeCue(cue as unknown as VTTCue, true);
    expect(cue.line).toBe(CUE_LINE_COARSE);
    expect(CUE_LINE_COARSE).toBeLessThan(CUE_LINE_FINE);
  });
});
