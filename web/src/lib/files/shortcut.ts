// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Node } from "./types";

// OpenShortcut is what opening a shortcut does. A reachable target opens at its own address, which is the caller's
// own access at work (a shortcut grants nothing). Anything else asks for access on the shortcut itself: the server files
// the request on the target. The target's ID is in the first answer only, and only when the caller reaches it.
export type OpenShortcut = { kind: "go"; to: string } | { kind: "unavailable" };

// openShortcut decides how a shortcut opens. A shortcut with no target is unavailable, however the API answered.
export function openShortcut(n: Node): OpenShortcut {
  if (n.unavailable || !n.target) return { kind: "unavailable" };
  return { kind: "go", to: `/n/${n.target.id}` };
}
