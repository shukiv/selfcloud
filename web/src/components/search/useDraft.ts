// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from "react";

// useDraft holds the words a person is typing and writes them out after a short pause, or at once on Enter. The address is
// the page's state, so the value the address holds is the one shown, unless the person is typing: a change of the address
// that was not made here (Back, a link, the other box) replaces the draft, but the echo of this box's own write does not,
// so a letter typed during the pause is not lost.
// scope is the place the box is in (the address's path): a pending write is dropped when the place changes, so a pause
// that ends after a click on a nav link does not move the person to the search page.
export function useDraft(urlValue: string, commit: (value: string) => void, pauseMs = 300, scope = "") {
  const [draft, setDraft] = useState(urlValue);
  const draftRef = useRef(urlValue);
  const written = useRef(urlValue);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const commitRef = useRef(commit);
  commitRef.current = commit;

  const cancel = useCallback(() => {
    if (timer.current !== null) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  }, []);

  useEffect(() => {
    if (urlValue === written.current) return;
    written.current = urlValue;
    draftRef.current = urlValue;
    cancel();
    setDraft(urlValue);
  }, [urlValue, cancel]);

  useEffect(() => cancel, [cancel]);
  useEffect(() => {
    cancel();
  }, [scope, cancel]);

  const send = useCallback(
    (value: string) => {
      cancel();
      written.current = value;
      commitRef.current(value);
    },
    [cancel],
  );

  const change = useCallback(
    (value: string) => {
      draftRef.current = value;
      setDraft(value);
      cancel();
      timer.current = setTimeout(() => {
        timer.current = null;
        send(value);
      }, pauseMs);
    },
    [cancel, send, pauseMs],
  );

  // submit writes the draft at once: Enter.
  const submit = useCallback(() => send(draftRef.current), [send]);

  return { draft, change, submit };
}
