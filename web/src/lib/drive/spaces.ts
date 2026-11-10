// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useMemo } from "react";
import { folders, hiddenFolders, loadHiddenFolders } from "../files/folders";
import type { FolderAccess } from "../files/types";
import { useStore } from "../store";

// useLocationSpaces is the spaces a drive list names a location from: the spaces the caller shows, and the ones it has hidden,
// which it reaches too (tl4 r1, finding 6). The hidden list is read once a session starts. The shown list is left as it is, so a
// space picker still lists only what the caller shows.
export function useLocationSpaces(signedIn: boolean): FolderAccess[] {
  const shown = useStore(folders);
  const hidden = useStore(hiddenFolders);
  useEffect(() => {
    if (signedIn) void loadHiddenFolders().catch(() => undefined);
  }, [signedIn]);
  return useMemo(() => [...shown, ...hidden], [shown, hidden]);
}
