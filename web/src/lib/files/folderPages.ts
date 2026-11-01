// SPDX-License-Identifier: AGPL-3.0-or-later

import { childrenPage } from "./api";

// folderPageReader reads the pages of a folder's listing, for the playlist's pager. The cursor of a page belongs to the
// sort the listing was read with, so every page is asked for with that same sort: the server refuses a cursor that does
// not match its sort (childCursorBad), and the pager would then stop for good.
export function folderPageReader(folder: string, dir: string | null, sort: Parameters<typeof childrenPage>[3]): (cursor: string) => ReturnType<typeof childrenPage> {
  return (cursor: string) => childrenPage(folder, dir, cursor, sort);
}
