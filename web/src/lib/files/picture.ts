// SPDX-License-Identifier: AGPL-3.0-or-later

// The picture choice of the player's More menu: how the video fits its box, and its brightness, contrast and
// saturation in percent. It is kept per person in this browser (lib/prefs), as the subtitle size is, so a new video
// and a new element take the kept values. A stored value is clamped to 50-150 on the step of 5; anything that does
// not read gives the default. Audio is never styled: the style is for the video element alone.

import { readPref, writePref } from "../prefs";

export interface Picture {
  fit: "contain" | "cover" | "fill";
  brightness: number;
  contrast: number;
  saturation: number;
}

export const DEFAULT_PICTURE: Picture = { fit: "contain", brightness: 100, contrast: 100, saturation: 100 };

// The percentages rest between 50 and 150, on the step of 5.
const LO = 50;
const HI = 150;
const STEP = 5;

const FITS: readonly Picture["fit"][] = ["contain", "cover", "fill"];

function keyOf(userId: string | null): string {
  return `selfcloud.picture.${userId ?? "visitor"}`;
}

// clampPercent brings one percentage to the range and the step: 123 rests at 125, 200 at 150.
function clampPercent(v: number): number {
  const rounded = Math.round(v / STEP) * STEP;
  return Math.min(HI, Math.max(LO, rounded));
}

// normal reads a stored picture: the fit must be one of the three and each percentage a finite number, else the whole
// choice is the default; a readable one is clamped to the range and the step.
function normal(raw: unknown): Picture {
  if (typeof raw !== "object" || raw === null) return DEFAULT_PICTURE;
  const p = raw as Record<string, unknown>;
  if (!FITS.includes(p.fit as Picture["fit"])) return DEFAULT_PICTURE;
  for (const k of ["brightness", "contrast", "saturation"] as const) {
    if (typeof p[k] !== "number" || !Number.isFinite(p[k])) return DEFAULT_PICTURE;
  }
  return {
    fit: p.fit as Picture["fit"],
    brightness: clampPercent(p.brightness as number),
    contrast: clampPercent(p.contrast as number),
    saturation: clampPercent(p.saturation as number),
  };
}

// readPicture reads the person's kept choice; anyone else's key is never read. A storage that refuses, a bad text or
// a bad value gives the default.
export function readPicture(userId: string | null): Picture {
  const raw = readPref(keyOf(userId));
  if (raw === null) return DEFAULT_PICTURE;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return DEFAULT_PICTURE;
  }
  return normal(parsed);
}

// writePicture keeps the choice under the person's own key, clamped as a read is, so nothing outside the range or the
// step, and no unknown fit, is ever stored. A storage that refuses keeps the choice for this page.
export function writePicture(userId: string | null, p: Picture): void {
  writePref(keyOf(userId), JSON.stringify(normal(p)));
}

// pictureStyle is the style of the video element: the fit as objectFit, and the three percentages as one filter. At
// the default of all three the filter is none, so the element keeps the browser's own drawing.
export function pictureStyle(p: Picture): { objectFit: Picture["fit"]; filter: string } {
  const filter =
    p.brightness === 100 && p.contrast === 100 && p.saturation === 100
      ? "none"
      : `brightness(${p.brightness}%) contrast(${p.contrast}%) saturate(${p.saturation}%)`;
  return { objectFit: p.fit, filter };
}
