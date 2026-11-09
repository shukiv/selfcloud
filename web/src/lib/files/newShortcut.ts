// SPDX-License-Identifier: AGPL-3.0-or-later

// The New chord (design-nt2-phase1, Menu and shortcut rules): Alt+C, then one letter that runs a New action. The reader is pure: it
// takes the state, the key and the time, and says what to do. A key is read by its position (event.code: KeyF, KeyU, ...), so it
// is the same key on a Hebrew or Arabic layout. The chord is armed for CHORD_WINDOW_MS; a key that is not in the chord cancels it.
import { isTextField } from "./keys";

// CHORD_WINDOW_MS is how long Alt+C waits for its letter.
export const CHORD_WINDOW_MS = 2000;

// NewChordAction is what a chord letter runs: the actions of the New menu, and a blank document of each Office kind.
export type NewChordAction = "newFolder" | "upload" | "uploadFolder" | "docx" | "xlsx" | "pptx";

// CHORD_LETTER is the letter the menu shows for each action (Google's letters, decision 108).
export const CHORD_LETTER: Record<NewChordAction, string> = { newFolder: "F", upload: "U", uploadFolder: "I", docx: "T", xlsx: "S", pptx: "P" };

// The letters by key position. A Map, so no key name of the prototype is an action.
const BY_CODE = new Map<string, NewChordAction>([
  ["KeyF", "newFolder"],
  ["KeyU", "upload"],
  ["KeyI", "uploadFolder"],
  ["KeyT", "docx"],
  ["KeyS", "xlsx"],
  ["KeyP", "pptx"],
]);

// ChordEvent is the part of a keydown the reader needs. key (the typed character) is not read: the position is.
export interface ChordEvent {
  code: string;
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
}

// ChordState is the chord between two keys: armedAt is the time Alt+C was read, or null when no chord is armed.
export interface ChordState {
  armedAt: number | null;
}

export const IDLE: ChordState = { armedAt: null };

// ChordStep is what one key does: the new state, the action to run (or null), and whether the key is consumed (its default
// action, such as typing ç on a Mac, is then cancelled by the caller).
export interface ChordStep {
  state: ChordState;
  action: NewChordAction | null;
  consume: boolean;
}

// readChord reads one keydown. blocked is true while the key is in a text field or an overlay is open: the chord then does
// nothing, and an armed chord is dropped. A key with Ctrl or Meta is never part of the chord.
export function readChord(state: ChordState, e: ChordEvent, now: number, blocked: boolean): ChordStep {
  if (blocked || e.ctrlKey || e.metaKey) return { state: IDLE, action: null, consume: false };
  const armed = state.armedAt !== null && now - state.armedAt <= CHORD_WINDOW_MS;
  const starts = e.code === "KeyC" && e.altKey;
  if (!armed) {
    return starts ? { state: { armedAt: now }, action: null, consume: true } : { state: IDLE, action: null, consume: false };
  }
  const action = BY_CODE.get(e.code);
  if (action) return { state: IDLE, action, consume: true };
  // Alt+C again starts the window over; any other key cancels the chord and is left to the page.
  if (starts) return { state: { armedAt: now }, action: null, consume: true };
  return { state: IDLE, action: null, consume: false };
}

// The elements that mean an overlay is open: a dialog, a drawer, a menu, a modal. A closed one is hidden (antd's hidden classes or
// display none), and is not counted.
// A menu is an overlay unless it is the sidebar's own menu (.shell-menu, always shown). A popover (Popover, Popconfirm) is an
// overlay while it is shown; its hidden class is ant-popover-hidden.
const OVERLAY = '[role="dialog"], [role="menu"]:not(.shell-menu), .ant-drawer-open, .ant-modal-wrap, .ant-dropdown, .ant-popover';
const HIDDEN = ".ant-dropdown-hidden, .ant-modal-wrap-hidden, .ant-popover-hidden, [hidden]";

// shown is true when the element and each ancestor is drawn: none of them has display none (a class from a stylesheet counts too).
function shown(el: Element): boolean {
  if (el.closest(HIDDEN)) return false;
  for (let node: Element | null = el; node; node = node.parentElement) {
    if (node.getAttribute("style")?.includes("display: none")) return false;
    if (typeof window !== "undefined" && window.getComputedStyle(node).display === "none") return false;
  }
  return true;
}

// overlayOpen says whether a dialog, drawer or menu is open in root.
export function overlayOpen(root: ParentNode = document): boolean {
  return [...root.querySelectorAll(OVERLAY)].some(shown);
}

// chordBlocked says whether the chord is ignored for a key on target: a text field, or any open overlay.
export function chordBlocked(target: EventTarget | null, root: ParentNode = document): boolean {
  return isTextField(target) || overlayOpen(root);
}
