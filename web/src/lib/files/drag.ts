// SPDX-License-Identifier: AGPL-3.0-or-later

import { can } from "./roles";
import type { Node } from "./types";

// The rules of the drag and the rubber band, as pure functions: the page feeds them what the pointer is over and what the
// toolbar's own action list says, and they answer. Nothing here touches the document.

// Dest is a place that can take items: a folder's root (dir null) or a directory. role is the caller's role there. path lists the
// ids from the top down to this place, itself included, when the place sits in a tree (a node of the folder tree), so a folder
// dropped on its own descendant is seen.
export interface Dest {
  folder: string;
  dir: string | null;
  role: string | undefined;
  path?: string[];
}

// Perms is what the toolbar's Move and Copy actions say about the dragged set: each is the action's own enabled and not hidden.
export interface Perms {
  move: boolean;
  copy: boolean;
}

// Why is the reason a drop is refused. role: the caller may not add to the place. self: a folder would go into itself or into
// something below it. here: every item is already in the place. move: the caller may not move these items (a copy may be allowed).
export type Why = "role" | "self" | "here" | "move";

export type Decision = { ok: true; mode: "move" | "copy"; nodes: Node[] } | { ok: false; why: Why };

// alreadyIn says an item is in the place now, so moving it there changes nothing.
export function alreadyIn(n: Node, dest: Pick<Dest, "folder" | "dir">): boolean {
  return n.folder_id === dest.folder && (n.parent_id ?? null) === dest.dir;
}

// intoItself says a folder is the place, or the place is below it.
export function intoItself(n: Node, dest: Dest): boolean {
  return n.kind === "dir" && (dest.dir === n.id || (dest.path?.includes(n.id) ?? false));
}

// dropDecision says what dropping the dragged items on a place does. copy is the Ctrl key. The place needs the same role as a
// paste does (can.create); a move needs the toolbar's Move to be enabled for the items, a copy its Copy. Items already in the
// place are left out; if none is left the drop is refused. A folder into itself or below it refuses the whole drop.
export function dropDecision(nodes: Node[], dest: Dest, copy: boolean, perms: Perms): Decision {
  if (!can.create(dest.role)) return { ok: false, why: "role" };
  if (nodes.some((n) => intoItself(n, dest))) return { ok: false, why: "self" };
  const rest = nodes.filter((n) => !alreadyIn(n, dest));
  if (rest.length === 0) return { ok: false, why: "here" };
  if (copy ? !perms.copy : !perms.move) return { ok: false, why: "move" };
  return { ok: true, mode: copy ? "copy" : "move", nodes: rest };
}

// Box is a rectangle in any one coordinate space.
export interface Box {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

// boxBetween is the rectangle that two corners make, whichever corner is first.
export function boxBetween(ax: number, ay: number, bx: number, by: number): Box {
  return { x0: Math.min(ax, bx), y0: Math.min(ay, by), x1: Math.max(ax, bx), y1: Math.max(ay, by) };
}

// touches says two rectangles share some area. A rectangle that only meets the other's edge does not touch it.
export function touches(a: Box, b: Box): boolean {
  return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;
}

// bandSelection is the ids a rubber band selects: the items its rectangle touches, in list order, and with additive the ones
// that were selected before the band began.
export function bandSelection(items: Array<{ id: string; box: Box }>, band: Box, base: string[], additive: boolean): string[] {
  const keep = new Set(items.filter((i) => touches(i.box, band)).map((i) => i.id));
  if (additive) for (const id of base) keep.add(id);
  // List order, so the selection reads the way the list does. A selected id the list no longer has is not kept.
  return items.filter((i) => keep.has(i.id)).map((i) => i.id);
}

// THRESHOLD is how far the pointer moves before a press becomes a drag or a band; a click that wobbles stays a click.
export const THRESHOLD = 5;

export function movedFar(dx: number, dy: number): boolean {
  return Math.hypot(dx, dy) >= THRESHOLD;
}

// edgeScroll says how far to scroll for a pointer at y, in a visible band from top to bottom: nothing in the middle, faster the
// nearer the edge, and a little beyond it. The zone is 48 pixels deep and the most is 24 pixels a frame.
export function edgeScroll(y: number, top: number, bottom: number): number {
  const zone = 48;
  if (y < top + zone) return -Math.min(24, Math.ceil(((top + zone - y) / zone) * 24));
  if (y > bottom - zone) return Math.min(24, Math.ceil(((y - (bottom - zone)) / zone) * 24));
  return 0;
}
