// SPDX-License-Identifier: AGPL-3.0-or-later

import { userWritable } from "../store";

// treePlace is the place the browser shows, for the folder tree in the sidebar. The browser (FilesPage) writes it when a
// folder is shown; the sidebar, which is not part of that page, reads it. It keeps the last place when the person leaves
// the files pages, so the tree still knows a space's top folder elsewhere; the sidebar marks the current folder only on
// the files pages. It is cleared at a change of person, as every userWritable store is.
export interface TreePlace {
  // folder is the space the place is in; chain is the directories below its root down to the one shown.
  folder: string;
  chain: string[];
  // top is the folder the person reaches the space by, when the space's own root is not theirs.
  top?: { id: string; name: string; role: string | undefined };
}

export const treePlace = userWritable<TreePlace | null>(null);
