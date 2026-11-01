// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's keys and its numbers, kept apart from the element so they can be tested. The player reads a key press
// with keyCommand, and skips it when the press was typed into a field or aimed at a control that takes the key itself
// (ignoredTarget). The clock is never read here: a time is a number the element reports.

// Command is what a key asks the player to do.
export type Command =
  | "toggle"
  | "back10"
  | "forward10"
  | "back5"
  | "forward5"
  | "volumeUp"
  | "volumeDown"
  | "mute"
  | "fullscreen"
  | "next"
  | "previous";

// refusedPlay says whether a play() that failed was refused for want of a gesture (the browser's autoplay rule): the one
// rejection the person can fix by pressing Play. Any other rejection is not this.
export function refusedPlay(err: unknown): boolean {
  return typeof err === "object" && err !== null && (err as { name?: unknown }).name === "NotAllowedError";
}

// SPEEDS are the playback rates the speed menu offers.
export const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2];

// VOLUME_STEP is how far one press of Up or Down moves the volume (0 to 1).
export const VOLUME_STEP = 0.05;

// HIDE_MS is how long the controls stay on screen after the last pointer move, while a video plays.
export const HIDE_MS = 2500;

// keyCommand says what a key press asks for, or null when the key means nothing to the player. A modified press (Ctrl,
// Alt, Meta) is not the player's: it belongs to the browser or to a shortcut of the page.
export function keyCommand(e: { key: string; altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean }): Command | null {
  if (e.altKey || e.ctrlKey || e.metaKey) return null;
  switch (e.key) {
    case " ":
    case "Spacebar":
    case "k":
    case "K":
      return "toggle";
    case "j":
    case "J":
      return "back10";
    case "l":
    case "L":
      return "forward10";
    case "ArrowLeft":
      return "back5";
    case "ArrowRight":
      return "forward5";
    case "ArrowUp":
      return "volumeUp";
    case "ArrowDown":
      return "volumeDown";
    case "m":
    case "M":
      return "mute";
    case "f":
    case "F":
      return "fullscreen";
    case "n":
    case "N":
      return "next";
    case "p":
    case "P":
      return "previous";
    default:
      return null;
  }
}

// Keys a control takes for itself: a slider moves on arrows, and a menu moves on arrows. The player leaves them to the
// control. Space is not among them: a slider does not use it.
const TAKES_ARROWS = new Set(["slider", "listbox", "menu", "menuitem", "option", "spinbutton"]);
// The controls Space presses (a button, a link, a box, a tab, a menu item, a list that opens on Space).
const TAKES_SPACE = new Set(["button", "link", "checkbox", "radio", "switch", "tab", "option", "menuitem", "combobox"]);

// keyboardFocus says whether the element has the focus the keyboard gave it (the browser's :focus-visible): Tab brings
// it, a pointer press does not. A browser that cannot answer says no, so Space stays with the player.
function keyboardFocus(el: Element): boolean {
  try {
    return el.matches(":focus-visible");
  } catch {
    return false;
  }
}

// ignoredTarget says whether the player must leave a key press to the element it was aimed at: anything typed into a
// field, or a control that uses the key itself. The element is read through its tag and its role, so the rule holds
// in every language.
//
// The rule for Space: the player plays and pauses on it, except where the person's keyboard is on a control whose own
// Space is what they asked for (a button Tab brought them to, a link, a box, a menu). A press with the pointer leaves the
// focus on the control it hit, but that is not keyboard focus, so Space still plays or pauses after a click.
export function ignoredTarget(target: EventTarget | null, key: string): boolean {
  if (!target || typeof (target as Element).closest !== "function") return false;
  const el = target as HTMLElement;
  // jsdom and older browsers do not compute isContentEditable, so the attribute is read as well.
  if (el.isContentEditable || el.closest('[contenteditable]:not([contenteditable="false"])')) return true;
  const tag = el.tagName;
  // Only true text entry takes the keys. A combobox input (the speed list, an antd Select) does not take the typed keys:
  // its keys are the player's, and it is the list's own role that says so.
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT" && isTextEntry(el as HTMLInputElement)) return true;
  const role = el.closest("[role]")?.getAttribute("role") ?? "";
  if (TAKES_ARROWS.has(role) && key.startsWith("Arrow")) return true;
  if (key === " " || key === "Spacebar") {
    if (!keyboardFocus(el)) return false;
    if (tag === "BUTTON" || tag === "A") return true;
    if (TAKES_SPACE.has(role)) return true;
  }
  return false;
}

// formatTime writes a time as m:ss, or h:mm:ss from an hour on. A time that is not a number (no media yet) is 0:00.
export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0;
  const whole = Math.floor(seconds);
  const h = Math.floor(whole / 3600);
  const m = Math.floor((whole % 3600) / 60);
  const s = whole % 60;
  const ss = String(s).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

// bufferedEnd is where the buffered range that holds the place reaches, or where the last range ends when the place is in
// none of them. The seek bar shows it as the part that is ready to play without a new request.
export function bufferedEnd(ranges: { length: number; start(i: number): number; end(i: number): number }, at: number): number {
  let end = 0;
  for (let i = 0; i < ranges.length; i++) {
    const s = ranges.start(i);
    const e = ranges.end(i);
    if (at >= s && at <= e) return e;
    end = Math.max(end, e);
  }
  return end;
}

// clamp keeps a number in a range.
export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

// playerPrefs are the volume, mute and speed the person chose. They stay for the page's life, so a file that starts
// after another plays at the same volume and speed (the player sets them on each element it makes).
// The volume and the mute are also kept in the browser, as a convenience: a reload keeps them. Storage can be refused or
// unreadable (a private window, blocked site data), and then the defaults hold and nothing else changes. The speed is not
// kept: it is the page's own choice each time.
const PREFS_KEY = "selfcloud.player";

export const playerPrefs = { ...loadVolumePrefs(), rate: 1 };

function loadVolumePrefs(): { volume: number; muted: boolean } {
  try {
    const raw = window.localStorage.getItem(PREFS_KEY);
    const v = raw ? (JSON.parse(raw) as { volume?: unknown; muted?: unknown }) : null;
    if (v && typeof v.volume === "number" && typeof v.muted === "boolean") return { volume: clamp(v.volume, 0, 1), muted: v.muted };
  } catch {
    // Unreadable storage: the defaults.
  }
  return { volume: 1, muted: false };
}

// saveVolumePrefs writes the volume and the mute, and does nothing when storage refuses.
export function saveVolumePrefs(): void {
  try {
    window.localStorage.setItem(PREFS_KEY, JSON.stringify({ volume: playerPrefs.volume, muted: playerPrefs.muted }));
  } catch {
    // Refused storage: the choice lasts for this page only.
  }
}

// isTextEntry says whether an input takes typed text. Buttons, boxes and sliders do not, and neither does a combobox
// (the speed list) or a read-only field.
function isTextEntry(el: HTMLInputElement): boolean {
  const nonText = ["button", "submit", "reset", "checkbox", "radio", "range", "file", "image", "color"];
  if (nonText.includes(el.type)) return false;
  if (el.getAttribute("role") === "combobox" || el.readOnly) return false;
  return true;
}
