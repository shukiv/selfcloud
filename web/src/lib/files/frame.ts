// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame the player's More menu saves: the picture on screen at the place the person is at, kept as a PNG in the
// person's own My files. The name is the file's own without its last extension, a space, the place floored to m-ss
// under an hour or h-mm-ss from an hour, and .png; the whole name rests within 255 UTF-8 bytes, the base cut on a code
// point boundary so the time and the extension are never cut. The capture draws on a canvas of the video's own size,
// never put in the document; a video with no picture yet is not_ready, and a canvas the browser will not read (a
// tainted one, or one that gives no blob) is unreadable.

// MAX_NAME is the whole name's budget in UTF-8 bytes, the file system's own limit for one name.
const MAX_NAME = 255;

const encoder = new TextEncoder();

// FrameError is the capture's refusal, with its reason: not_ready while the video has no picture yet, unreadable when
// the browser will not give the canvas's bytes. The menu says one text per reason.
export class FrameError extends Error {
  readonly reason: "not_ready" | "unreadable";

  constructor(reason: "not_ready" | "unreadable") {
    super(reason);
    this.name = "FrameError";
    this.reason = reason;
  }
}

// Frames is the More menu's way to keep the frame on screen: the dock gives it only to a signed-in person with a
// My files space, and its save queues the PNG on the shared upload manager (decision 231).
export interface Frames {
  save(blob: Blob, seconds: number): void;
}

// pad writes a number as two digits, the seconds and the minutes of an hour's own place.
function pad(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

// frameName is the kept frame's name: the file's name without its last extension (a name whose only dot is its first
// character keeps it whole), a space, the place floored, and .png. A place that is NaN, Infinity or negative reads as
// zero. The base is cut on a code point boundary to what the time and the extension leave of the 255 bytes.
export function frameName(fileName: string, seconds: number): string {
  const dot = fileName.lastIndexOf(".");
  const base = dot > 0 ? fileName.slice(0, dot) : fileName;
  const whole = Number.isFinite(seconds) && seconds >= 0 ? Math.floor(seconds) : 0;
  const h = Math.floor(whole / 3600);
  const m = Math.floor((whole % 3600) / 60);
  const s = whole % 60;
  const time = h > 0 ? `${h}-${pad(m)}-${pad(s)}` : `${m}-${pad(s)}`;
  const suffix = ` ${time}.png`;
  const budget = MAX_NAME - encoder.encode(suffix).length;
  let cut = "";
  let used = 0;
  for (const ch of base) {
    const n = encoder.encode(ch).length;
    if (used + n > budget) break;
    cut += ch;
    used += n;
  }
  return cut + suffix;
}

// captureFrame draws the video's current frame on a canvas of its own videoWidth and videoHeight, never in the
// document, and keeps it as a PNG. A video with no size or no data yet (readyState under 2) is not_ready; a draw or a
// toBlob the browser refuses, and a toBlob that gives no blob, are unreadable.
export function captureFrame(video: HTMLVideoElement): Promise<Blob> {
  if (video.videoWidth === 0 || video.videoHeight === 0 || video.readyState < 2) {
    return Promise.reject(new FrameError("not_ready"));
  }
  const canvas = document.createElement("canvas");
  canvas.width = video.videoWidth;
  canvas.height = video.videoHeight;
  try {
    const ctx = canvas.getContext("2d");
    if (!ctx) return Promise.reject(new FrameError("unreadable"));
    ctx.drawImage(video, 0, 0);
  } catch {
    return Promise.reject(new FrameError("unreadable"));
  }
  return new Promise<Blob>((resolve, reject) => {
    try {
      canvas.toBlob((blob) => {
        if (blob === null) reject(new FrameError("unreadable"));
        else resolve(blob);
      }, "image/png");
    } catch {
      reject(new FrameError("unreadable"));
    }
  });
}
