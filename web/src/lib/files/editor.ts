// SPDX-License-Identifier: AGPL-3.0-or-later

// The office editor in the web interface (decision 72). The page opens an office file in Collabora Online: it asks the
// API for the editor's address and a token (POST /api/v1/nodes/{nod}/office), then posts the token into a hidden form whose
// target is the editor's frame, so the token is in a request body and never in an address. Messages from the editor are
// trusted only from the editor's own origin and window. The server offers the feature in /api/v1/info (office_edit).

import { writable } from "../store";
import { kindOf } from "./kind";
import { can } from "./roles";
import type { Node } from "./types";

// EDIT_FEATURE is the name the server gives the editor in /api/v1/info, while office.enabled is on.
export const EDIT_FEATURE = "office_edit";

// officeEdit is true while the server offers the editor. It is false until the start-up read has answered.
export const officeEdit = writable(false);

// EDITOR_FRAME is the name of the frame the editor's form targets.
export const EDITOR_FRAME = "selfcloud-office-editor";

// EditorMode is the editor's mode: edit for a person who may save, view for one who may only read.
export type EditorMode = "edit" | "view";

// editorEntry is the mode the page offers for a node, or null when no editor entry is offered: while the feature is off,
// for a folder, and for a file that is not an office document. An editor (or more) gets edit; anyone else gets view.
export function editorEntry(n: Node, featureOn: boolean): EditorMode | null {
  if (!featureOn || n.kind !== "file" || kindOf(n) !== "office") return null;
  return can.edit(n.role) ? "edit" : "view";
}

// safeFrom accepts a place to return to only when it is a path of this app: it starts with one slash, and is not
// another host (//host), a backslash path, or a string with a control character in it.
export function safeFrom(v: string | null): string | null {
  if (v === null || !v.startsWith("/") || v.startsWith("//") || v.includes("\\")) return null;
  if (/[\u0000-\u001f\u007f]/.test(v)) return null;
  return v;
}

// editorPath is the address of the editor page for a node. from is the place to return to when the editor closes. tab says
// the page was opened in a new tab by a script (window.open), so its close may end the tab (oe1).
export function editorPath(nod: string, mode: EditorMode, from: string | null, tab = false): string {
  const q = new URLSearchParams({ mode });
  const back = safeFrom(from);
  if (back !== null) q.set("from", back);
  if (tab) q.set("tab", "1");
  return `/edit/${encodeURIComponent(nod)}?${q.toString()}`;
}

// readEditorPath reads the mode, the place to return to and the new-tab flag from the page's query. Anything but view is edit.
export function readEditorPath(search: string): { mode: EditorMode; from: string | null; tab: boolean } {
  const q = new URLSearchParams(search);
  return { mode: q.get("mode") === "view" ? "view" : "edit", from: safeFrom(q.get("from")), tab: q.get("tab") === "1" };
}

// coarsePointer says whether the main pointer is a finger, read when called (the page's hook is for drawing). A phone or a
// tablet opens an office file in the same tab (oe1).
export function coarsePointer(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
}

// openEditorTab opens an office file in the editor, full page (oe1). On a fine pointer it opens a new tab with
// window.open and noopener, so the new tab cannot reach this one; it must be called from the person's click or key event,
// or the browser blocks it. On a coarse pointer it goes to the editor in this tab, with from set to here, the place to
// return to. The new tab's address carries tab=1, so its close may end the tab.
export function openEditorTab(
  nod: string,
  mode: EditorMode,
  here: string,
  coarse: boolean,
  goTo: (to: string) => void,
  open: (url: string, target: string, features: string) => unknown = (u, t, f) => window.open(u, t, f),
): void {
  if (coarse) {
    goTo(editorPath(nod, mode, here));
    return;
  }
  open(editorPath(nod, mode, null, true), "_blank", "noopener");
}

// LinkClick is the part of a mouse event officeLinkClick reads.
export interface LinkClick {
  button: number;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  preventDefault: () => void;
}

// officeLinkClick is the click on a link to a file (a row on a page, a search hit). An office file the editor offers opens in
// the editor tab, not at the link's address; a modified click (ctrl, shift, alt, or a middle button) keeps the browser's own
// choice. Anything else is left to the link.
export function plainClick(e: LinkClick): boolean {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;
}

export function officeLinkClick(e: LinkClick, n: Node, featureOn: boolean, here: string, goTo: (to: string) => void): void {
  if (!plainClick(e)) return;
  const mode = editorEntry(n, featureOn);
  if (mode === null) return;
  e.preventDefault();
  openEditorTab(n.id, mode, here, coarsePointer(), goTo);
}

// SaveState is the save state the editor's bar shows. null is no state yet. No documented message says a save has started,
// so there is no "saving" state (decision 102).
export type SaveState = "saved" | "unsaved" | "failed" | null;

// saveStateAfter gives the save state after one message from the editor (decision 102). Doc_ModifiedStatus carries
// Values.Modified: true is unsaved changes, false is saved; the message may come again with the same value, which keeps the
// state. Action_Save_Resp carries Values.success: true is saved, false is save failed. Anything else changes nothing.
export function saveStateAfter(prev: SaveState, msg: EditorMessage): SaveState {
  if (msg.id === "Doc_ModifiedStatus") {
    const m = msg.values?.Modified;
    if (m === true) return "unsaved";
    if (m === false) return "saved";
    return prev;
  }
  if (msg.id === "Action_Save_Resp") {
    const ok = msg.values?.success;
    if (ok === true) return "saved";
    if (ok === false) return "failed";
  }
  return prev;
}

// editorOrigin is the origin of the editor's action URL, the only origin its messages may come from. Null when the URL
// is not an http or https address.
export function editorOrigin(actionURL: string): string | null {
  try {
    const u = new URL(actionURL);
    if (u.protocol !== "https:" && u.protocol !== "http:") return null;
    return u.origin;
  } catch {
    return null;
  }
}

// EditorMessage is a message the editor sends: its MessageId, and its Values when it has any.
export interface EditorMessage {
  id: string;
  values: Record<string, unknown> | undefined;
}

// parseEditorMessage reads a message whether it arrives as JSON text or as an object. Anything that is not an object with a
// MessageId is no message.
export function parseEditorMessage(data: unknown): EditorMessage | null {
  let v: unknown = data;
  if (typeof v === "string") {
    try {
      v = JSON.parse(v);
    } catch {
      return null;
    }
  }
  if (typeof v !== "object" || v === null || Array.isArray(v)) return null;
  const rec = v as { MessageId?: unknown; Values?: unknown };
  if (typeof rec.MessageId !== "string" || rec.MessageId === "") return null;
  const values = typeof rec.Values === "object" && rec.Values !== null ? (rec.Values as Record<string, unknown>) : undefined;
  return { id: rec.MessageId, values };
}

// isTrustedEditorMessage says a message may be read: it came from the editor's origin and from the editor's own frame.
// With no origin or no frame, nothing is trusted.
export function isTrustedEditorMessage(
  ev: { origin: string; source: unknown },
  expectedOrigin: string | null,
  frame: unknown,
): boolean {
  if (expectedOrigin === null || frame === null || frame === undefined) return false;
  return ev.origin === expectedOrigin && ev.source === frame;
}

// buildEditorForm makes the hidden form that posts the token into the editor. The token and the expiry (milliseconds since the
// epoch) are inputs of the body; the action is the editor's address as the server gave it, with no token in it.
export function buildEditorForm(doc: Document, actionURL: string, token: string, expiresMs: number, target: string): HTMLFormElement {
  const form = doc.createElement("form");
  form.method = "post";
  form.action = actionURL;
  form.target = target;
  form.style.display = "none";
  const tok = doc.createElement("input");
  tok.type = "hidden";
  tok.name = "access_token";
  tok.setAttribute("value", token);
  const ttl = doc.createElement("input");
  ttl.type = "hidden";
  ttl.name = "access_token_ttl";
  ttl.setAttribute("value", String(expiresMs));
  form.append(tok, ttl);
  return form;
}

// SaveAsAsk is the name the Save As dialog starts from: the file's name without its extension, and the extension, which the
// copy keeps unless the person types another.
export interface SaveAsAsk {
  base: string;
  ext: string;
}

// saveAsSuggestion splits a file's name for the Save As dialog. A name with no dot, or a dot first, has no extension.
export function saveAsSuggestion(name: string): SaveAsAsk {
  const i = name.lastIndexOf(".");
  return i > 0 ? { base: name.slice(0, i), ext: name.slice(i) } : { base: name, ext: "" };
}

// saveAsNameOk says a typed name may be sent: not empty after trimming, and no slash or backslash, which a file name cannot hold.
export function saveAsNameOk(typed: string): boolean {
  const v = typed.trim();
  return v !== "" && !/[\\/]/.test(v);
}

// saveAsFilename is the name sent in Action_SaveAs: the typed name, with the original extension added unless it is there.
export function saveAsFilename(typed: string, ext: string): string {
  const v = typed.trim();
  return ext !== "" && v.toLowerCase().endsWith(ext.toLowerCase()) ? v : v + ext;
}

// editorMessage is a message the host sends to the editor. It is sent as JSON text, as Collabora reads it.
export function editorMessage(id: string, values?: Record<string, unknown>): string {
  const msg: { MessageId: string; SendTime: number; Values?: Record<string, unknown> } = { MessageId: id, SendTime: Date.now() };
  if (values !== undefined) msg.Values = values;
  return JSON.stringify(msg);
}
