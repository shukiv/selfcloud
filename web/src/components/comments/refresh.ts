// SPDX-License-Identifier: AGPL-3.0-or-later

import type { QueryClient } from "@tanstack/react-query";
import { queryKey } from "../../lib/query";
import { commentFault } from "../../lib/comments/logic";

// refreshAfterChange brings the comments and the activity of an item up to date after a write. The tabs keep a visited
// pane mounted, so the activity is refreshed here as well, not only when its tab is opened.
export async function refreshAfterChange(qc: QueryClient, nodeID: string): Promise<void> {
  await Promise.all([
    qc.invalidateQueries({ queryKey: queryKey("comments", nodeID) }),
    qc.invalidateQueries({ queryKey: queryKey("activity", nodeID) }),
  ]);
}

// listIsStale says the refusal means the list on the screen is out of date: the comment is gone, the switch is off, or a
// reply went under a reply. The list is then read again, so the screen shows what the server has.
export function listIsStale(err: unknown): boolean {
  const f = commentFault(err);
  return f === "gone" || f === "off" || f === "reply";
}
