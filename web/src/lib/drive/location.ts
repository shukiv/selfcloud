// SPDX-License-Identifier: AGPL-3.0-or-later

// Where a drive item sits, for the Location column of the drive lists (tl4, decision 136). The server names the parent when the
// caller reaches it (place). Without a place, an item at the top of its space shows the space's own name, and any other item
// shows nothing: a parent the caller cannot see is not guessed at.
import type { FolderAccess } from "../files/types";

// Placed is what the rule reads of an item: its place, its space, and its parent (for a trashed item, the parent it was in).
export interface Placed {
  place?: string;
  folder_id: string;
  parent_id: string | null;
}

// locationOf is the text of an item's location. personal is the name the caller's own folder shows as (My files).
export function locationOf(item: Placed, folders: readonly FolderAccess[], personal: string): string {
  if (item.place) return item.place;
  const space = folders.find((f) => f.id === item.folder_id);
  // A space whose root the caller does not know cannot say which items are at its top, so nothing is shown for them.
  if (!space || !space.root_node_id || item.parent_id !== space.root_node_id) return "";
  return space.kind === "personal" ? personal : space.name;
}
