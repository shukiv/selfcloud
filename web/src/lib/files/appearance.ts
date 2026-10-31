// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from "./types";

// savedNode is the node as a colour or description save leaves it. The answer leaves out an empty colour or
// description (the server omits empty fields), so those are taken as cleared, not kept from before. The answer
// carries no role, so the caller's role is kept.
export function savedNode(node: Node, next: Node): Node {
  return { ...node, ...next, role: next.role ?? node.role, color: next.color, description: next.description };
}

// keepTargetOnSave applies a late save to the drawer only when the drawer is still open on that same item. A save
// answered after the person has opened another item must not change the one now shown.
export function keepTargetOnSave(current: Node | null, saved: Node, drawerOpen: boolean): Node | null {
  return drawerOpen && current !== null && current.id === saved.id ? saved : current;
}
