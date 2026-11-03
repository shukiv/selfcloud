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

// editorPath is the address of the editor page for a node. from is the place to return to when the editor closes.
export function editorPath(nod: string, mode: EditorMode, from: string | null): string {
  const q = new URLSearchParams({ mode });
  const back = safeFrom(from);
  if (back !== null) q.set("from", back);
  return `/edit/${encodeURIComponent(nod)}?${q.toString()}`;
}

// readEditorPath reads the mode and the place to return to from the page's query. Anything but view is edit.
export function readEditorPath(search: string): { mode: EditorMode; from: string | null } {
  const q = new URLSearchParams(search);
  return { mode: q.get("mode") === "view" ? "view" : "edit", from: safeFrom(q.get("from")) };
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
