// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame session's state (decision 214). A host application embeds the drive with a frame sign-in link
// (decision 197); the redeem answer and the session read name the origins that may frame the account's pages,
// and the page keeps that list here. The list is the only address the page ever posts to: one message per
// origin, never "*", and nothing but the message's type. The list lives in this module and in the tab's
// sessionStorage, so a reload of the frame keeps it; a storage that is refused keeps the module state only.

const FRAME_KEY = "selfcloud.frame";

// origins is the remembered list; null until a list is remembered or read back from the tab's storage.
let origins: string[] | null = null;
let restored = false;

// inFrame is true when the page is drawn inside a frame. A cross-origin parent makes the read throw; that
// too means a frame.
export function inFrame(): boolean {
  try {
    return window.self !== window.top;
  } catch {
    return true;
  }
}

// readStored reads the list back from the tab's storage. A storage that throws, or a value that is not a list
// of strings, is no list.
function readStored(): string[] | null {
  try {
    const raw = window.sessionStorage.getItem(FRAME_KEY);
    if (raw === null) return null;
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed) || !parsed.every((o): o is string => typeof o === "string")) return null;
    return parsed;
  } catch {
    return null;
  }
}

// rememberFrame keeps the origins a frame session's answer named. A write the storage refuses is swallowed:
// the list lasts for this page load.
export function rememberFrame(list: string[]): void {
  origins = list;
  restored = true;
  try {
    window.sessionStorage.setItem(FRAME_KEY, JSON.stringify(list));
  } catch {
    // The storage is refused; the module state keeps the list.
  }
}

// remembered is the list, read back from the tab's storage once when this page load has none yet.
function remembered(): string[] | null {
  if (!restored) {
    restored = true;
    origins = readStored();
  }
  return origins;
}

// frameSession is true when the page is in a frame and a list of host origins is remembered.
export function frameSession(): boolean {
  const list = remembered();
  return inFrame() && list !== null && list.length > 0;
}

// HostMessage is the whole of what the page ever tells its host: a type, and nothing else.
export type HostMessage = "selfcloud.session-ended" | "selfcloud.frame-blocked";

// postToHost tells the host application one message, once per remembered origin: the browser delivers a
// postMessage only to a parent whose origin matches, so each origin is addressed with its own post. Never
// "*", never any other data, and nothing when the page is not in a frame or no list is remembered.
export function postToHost(type: HostMessage): void {
  if (!inFrame()) return;
  const list = remembered();
  if (list === null) return;
  for (const origin of list) {
    try {
      window.parent.postMessage({ type }, origin);
    } catch {
      // An origin the browser refuses takes no message; the others still get theirs.
    }
  }
}

// hostLabel is the parent's host when it can be told and is in the remembered list: the first ancestor
// origin where the browser gives them, else the referrer's origin. Otherwise null, and the views name the
// application the drive is in instead of a host.
export function hostLabel(): string | null {
  const list = remembered();
  if (list === null || list.length === 0) return null;
  let origin: string | null = null;
  try {
    const ancestors = (window.location as Location & { ancestorOrigins?: { length: number; [index: number]: string } }).ancestorOrigins;
    if (ancestors && ancestors.length > 0) origin = ancestors[0] ?? null;
    if (origin === null && document.referrer !== "") origin = new URL(document.referrer).origin;
  } catch {
    origin = null;
  }
  if (origin === null || !list.includes(origin)) return null;
  try {
    return new URL(origin).host;
  } catch {
    return null;
  }
}
