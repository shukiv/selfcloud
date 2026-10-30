// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";

// useNativeChange commits a date input the way a native change does: on the change event, when the person has picked a
// whole date or left the field, and not on every keystroke (React's onChange fires on each input). It returns a ref for
// the element that holds the input. The listener is attached to the input inside it, and is re-attached when the input
// changes.
export function useNativeChange<T extends HTMLElement>(commit: (value: string) => void) {
  const wrap = useRef<T | null>(null);
  const latest = useRef(commit);
  latest.current = commit;
  useEffect(() => {
    const input = wrap.current?.querySelector("input");
    if (!input) return;
    const onChange = () => latest.current(input.value);
    input.addEventListener("change", onChange);
    return () => input.removeEventListener("change", onChange);
  });
  return wrap;
}
