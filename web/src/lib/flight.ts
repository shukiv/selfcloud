// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef } from 'react';

// Flight is the request a dialog has out. A dialog that is busy can still be cancelled: cancelling aborts the request,
// so the dialog closes at once instead of waiting for an answer that may never come.
export interface Flight {
  // start begins a request and returns the signal to pass to it.
  start: () => AbortSignal;
  // end forgets the request once it is answered.
  end: () => void;
  // abort aborts the request on its way, if any, and says whether one was out.
  abort: () => boolean;
}

export function useFlight(): Flight {
  const current = useRef<AbortController | null>(null);
  return {
    start: () => {
      const ctl = new AbortController();
      current.current = ctl;
      return ctl.signal;
    },
    end: () => {
      current.current = null;
    },
    abort: () => {
      const ctl = current.current;
      current.current = null;
      if (ctl === null) return false;
      ctl.abort();
      return true;
    },
  };
}
