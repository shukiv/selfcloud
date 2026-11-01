// SPDX-License-Identifier: AGPL-3.0-or-later

// Subtitles are read from a .vtt or .srt file beside a video, and turned into cues the player shows. Both formats are
// parsed here, in the browser: the server only names the files and serves their text. A block that is not a cue (a
// WEBVTT header, a NOTE, a STYLE or a REGION block, a number line) is skipped; a cue whose timing does not read, or whose
// end is not after its start, is skipped too. The text of a cue is kept as the file gave it: the browser's own cue
// renderer reads its markup, and it runs no script.

export interface Cue {
  start: number;
  end: number;
  text: string;
}

// MAX_CUES bounds a file's cues, so a file of millions of lines cannot fill the player.
export const MAX_CUES = 20_000;

// TIMING matches "[hh:]mm:ss.ttt --> [hh:]mm:ss.ttt" (WebVTT, with a dot) and the SRT form with a comma. Settings after
// the end time are ignored.
const TIMING = /^(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{3})\s*-->\s*(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{3})(?:\s.*)?$/;

// toSeconds reads the parts of one time. The minutes and the seconds are below 60.
function toSeconds(h: string | undefined, m: string, s: string, ms: string): number | null {
  const mins = Number(m);
  const secs = Number(s);
  if (mins > 59 || secs > 59) return null;
  return (h ? Number(h) : 0) * 3600 + mins * 60 + secs + Number(ms) / 1000;
}

// parseCues reads the cues of a subtitle text, in the order they come.
export function parseCues(raw: string): Cue[] {
  const text = raw.replace(/^﻿/, "").replace(/\r\n?/g, "\n");
  const out: Cue[] = [];
  for (const block of text.split(/\n{2,}/)) {
    const lines = block.split("\n");
    const at = lines.findIndex((l) => l.includes("-->"));
    if (at < 0) continue;
    const m = TIMING.exec(lines[at].trim());
    if (!m) continue;
    const start = toSeconds(m[1], m[2], m[3], m[4]);
    const end = toSeconds(m[5], m[6], m[7], m[8]);
    if (start === null || end === null || end <= start) continue;
    // The text ends at the last line that has words: the file's own final line break is not part of the cue.
    const body = lines.slice(at + 1).join("\n").replace(/\n+$/, "");
    out.push({ start, end, text: body });
    if (out.length >= MAX_CUES) break;
  }
  return out;
}

// SubtitleFormat is the format of a subtitle file. A .srt and a .vtt are read by the same parser.
export type SubtitleFormat = "vtt" | "srt";

// SubtitleTrack is one subtitle file read and parsed: its label (the file's name) and its cues.
export interface SubtitleTrack {
  label: string;
  cues: Cue[];
}

// MAX_SUBTITLE_BYTES is how much of a subtitle file is read. A file longer than this is cut: its first part still shows.
export const MAX_SUBTITLE_BYTES = 4 * 1024 * 1024;

// CUE_LINE_FINE and CUE_LINE_COARSE are where the bottom of a cue sits, as a percentage of the picture's height from its top.
// The bar of the player is at the bottom (about 10 % of the picture on a pointer, more on a phone), so the cue sits above it.
// The browser's own subtitle settings still win over this placement.
export const CUE_LINE_FINE = 80;
export const CUE_LINE_COARSE = 66;

// placeCue moves a cue up off the bottom edge, where the bar is drawn. coarse says the pointer is a touch.
export function placeCue(cue: VTTCue, coarse: boolean): void {
  cue.snapToLines = false;
  cue.line = coarse ? CUE_LINE_COARSE : CUE_LINE_FINE;
  cue.lineAlign = "end";
}


// LEGACY_CHARSET is the single-byte charset a subtitle file is read in when it is not UTF-8, chosen by the interface
// language. The Hebrew, Arabic and Cyrillic ones are the common legacy encodings of those scripts. Any other language reads
// Western Windows-1252. The choice is a guess from the interface, not a detection: a file in another charset reads wrong.
const LEGACY_CHARSET: Record<string, string> = { he: "windows-1255", ar: "windows-1256", ru: "windows-1251" };

// utf8Tail is how many bytes at the end of a cut UTF-8 file belong to a character whose other bytes are missing. It looks
// back at most three bytes, the longest a UTF-8 character takes beyond its lead byte.
function utf8Tail(b: Uint8Array): number {
  for (let back = 1; back <= 4 && back <= b.length; back++) {
    const byte = b[b.length - back];
    if ((byte & 0xc0) === 0x80) continue; // a continuation byte: keep looking for the lead
    const need = byte >= 0xf0 ? 4 : byte >= 0xe0 ? 3 : byte >= 0xc0 ? 2 : 1;
    return need > back ? back : 0;
  }
  return 0;
}

// decodeSubtitle turns the bytes of a subtitle file into text. A byte-order mark decides the encoding (UTF-8, or UTF-16 in
// either order). Otherwise the bytes are read as UTF-8 when they are valid UTF-8, and as the interface language's legacy
// charset when they are not. truncated says the bytes are the first part of a longer file, so a character cut at the end
// is dropped, not read as garbage.
export function decodeSubtitle(bytes: Uint8Array, lang: string, truncated = false): string {
  if (bytes[0] === 0xff && bytes[1] === 0xfe) return new TextDecoder("utf-16le").decode(bytes.subarray(2));
  if (bytes[0] === 0xfe && bytes[1] === 0xff) return new TextDecoder("utf-16be").decode(bytes.subarray(2));
  const bom = bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf;
  let body = bom ? bytes.subarray(3) : bytes;
  if (truncated) body = body.subarray(0, body.length - utf8Tail(body));
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(body);
  } catch {
    // Not UTF-8: the legacy charset of the interface language. A runtime without that charset reads Windows-1252.
    const legacy = LEGACY_CHARSET[lang] ?? "windows-1252";
    try {
      return new TextDecoder(legacy).decode(body);
    } catch {
      return new TextDecoder("windows-1252").decode(body);
    }
  }
}

// readTracks reads each subtitle file (in the order given) through open, which asks for a byte range and gives the response.
// A file the server refuses (403 for a file that may not be downloaded, or any failure) gives no track; it never fails the
// others. The bytes are decoded by decodeSubtitle, in the charset of the interface language lang when they are not UTF-8.
export async function readTracks(
  files: { id: string; name: string }[],
  open: (id: string, range: string) => Promise<Response>,
  lang = "en",
): Promise<SubtitleTrack[]> {
  const out: SubtitleTrack[] = [];
  for (const f of files) {
    try {
      const res = await open(f.id, `bytes=0-${MAX_SUBTITLE_BYTES - 1}`);
      if (!res.ok && res.status !== 416) continue;
      const bytes = res.status === 416 ? new Uint8Array() : new Uint8Array(await res.arrayBuffer());
      const text = decodeSubtitle(bytes, lang, bytes.length >= MAX_SUBTITLE_BYTES);
      out.push({ label: f.name, cues: parseCues(text) });
    } catch {
      // A file that cannot be read is left out; the others still show.
    }
  }
  return out;
}
