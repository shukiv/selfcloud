// SPDX-License-Identifier: AGPL-3.0-or-later

// The equalizer choice of the player's More menu: ten bands from 31 Hz to 16 kHz, each a gain in dB from -12 to 12 on
// the step of 1, and an On switch. It is kept per person in this browser (lib/prefs), as the picture is, so a new file
// and a new element take the kept choice. A stored value is clamped and rounded; anything that does not read gives the
// default. The four presets name the gains that match them; custom names any others. This half is pure: the Web Audio
// graph that applies the gains lives in eqGraph.ts, loaded only when it is needed.

import { readPref, writePref } from "../prefs";

// BANDS are the ten centre frequencies, in Hz: the low shelf, the eight peaking bands and the high shelf.
export const BANDS = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000];

export interface Eq {
  on: boolean;
  gains: number[];
}

export const DEFAULT_EQ: Eq = { on: false, gains: BANDS.map(() => 0) };

export type PresetName = "flat" | "bass" | "treble" | "vocal";

export const PRESETS: Record<PresetName, number[]> = {
  flat: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
  bass: [6, 5, 4, 2, 0, 0, 0, 0, 0, 0],
  treble: [0, 0, 0, 0, 0, 0, 2, 4, 5, 6],
  vocal: [-2, -2, -1, 1, 3, 3, 2, 1, 0, -1],
};

// presetOf names the preset the gains match, else custom.
export function presetOf(gains: number[]): PresetName | "custom" {
  for (const name of Object.keys(PRESETS) as PresetName[]) {
    const p = PRESETS[name];
    if (gains.length === p.length && p.every((v, i) => gains[i] === v)) return name;
  }
  return "custom";
}

// The gains rest between -12 and 12 dB, on the step of 1.
const LO = -12;
const HI = 12;

function keyOf(userId: string | null): string {
  return `selfcloud.equalizer.${userId ?? "visitor"}`;
}

// clampGain brings one gain to the range and the step: 2.4 rests at 2, 20 at 12.
function clampGain(v: number): number {
  return Math.min(HI, Math.max(LO, Math.round(v)));
}

// normal reads a stored choice: On must be a boolean and the gains ten finite numbers, else the whole choice is the
// default; a readable one is clamped to the range and the step.
function normal(raw: unknown): Eq {
  if (typeof raw !== "object" || raw === null) return DEFAULT_EQ;
  const e = raw as Record<string, unknown>;
  if (typeof e.on !== "boolean") return DEFAULT_EQ;
  if (!Array.isArray(e.gains) || e.gains.length !== BANDS.length) return DEFAULT_EQ;
  for (const g of e.gains) {
    if (typeof g !== "number" || !Number.isFinite(g)) return DEFAULT_EQ;
  }
  return { on: e.on, gains: (e.gains as number[]).map(clampGain) };
}

// readEq reads the person's kept choice; anyone else's key is never read. A storage that refuses, a bad text or a bad
// value gives the default.
export function readEq(userId: string | null): Eq {
  const raw = readPref(keyOf(userId));
  if (raw === null) return DEFAULT_EQ;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return DEFAULT_EQ;
  }
  return normal(parsed);
}

// writeEq keeps the choice under the person's own key, clamped as a read is, so nothing outside the range or the step,
// and no bad list, is ever stored. A storage that refuses keeps the choice for this page.
export function writeEq(userId: string | null, eq: Eq): void {
  writePref(keyOf(userId), JSON.stringify(normal(eq)));
}

// gainsOf is what the graph is set to: the choice's gains when On, all 0 when Off (the graph stays, so Off is a flat
// curve, not a removed one).
export function gainsOf(eq: Eq): number[] {
  return eq.on ? eq.gains : BANDS.map(() => 0);
}

// bandLabel names one band as the panel shows it: "31 Hz" ... "16 kHz".
export function bandLabel(hz: number): string {
  return hz < 1000 ? `${hz} Hz` : `${hz / 1000} kHz`;
}

// dbLabel names one gain as the panel shows it: "+3 dB", "0 dB", "-2 dB".
export function dbLabel(v: number): string {
  return `${v > 0 ? "+" : ""}${v} dB`;
}
