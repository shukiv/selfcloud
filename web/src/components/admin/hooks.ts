// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef, useState } from "react";
import { session, sessionKey } from "../../lib/session";
import { useStore } from "../../lib/store";

// useOnSessionChange runs clear when the signed-in person changes (a sign-out, or a sign-in as someone else). A secret a
// page holds is cleared this way, so the next person on the tab never sees it. clear runs while the change is rendered,
// not in an effect after the frame is committed, so no committed render shows the previous person's secret. clear must
// only set this page's own state (it does, in every caller).
// The previous key is kept in state (React's "store the previous value" pattern): when React drops a render, the state and
// the clear are dropped together, so the next render still sees the change and clears.
export function useOnSessionChange(clear: () => void): void {
  const key = sessionKey(useStore(session));
  const [seen, setSeen] = useState(key);
  if (seen !== key) {
    setSeen(key);
    clear();
  }
}

// useGuard returns run, which starts fn unless an earlier call is still out. A second click while a request is in
// flight does nothing, even before the page has drawn the disabled button.
export function useGuard(): (fn: () => Promise<void>) => Promise<void> {
  const busy = useRef(false);
  return (fn) => {
    if (busy.current) return Promise.resolve();
    busy.current = true;
    return fn().finally(() => {
      busy.current = false;
    });
  };
}
