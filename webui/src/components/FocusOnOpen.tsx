// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect } from "react";

// FocusOnOpen focuses the element the selector names once its dialog has opened. It is rendered inside the dialog's
// body, so it runs each time the dialog opens; antd's own open hooks do not run in every environment, and a focus
// set before the dialog's focus trap settles is taken back, so the focus waits a tick.
export default function FocusOnOpen({ selector }: { selector: string }) {
  useEffect(() => {
    const id = window.setTimeout(() => document.querySelector<HTMLElement>(selector)?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [selector]);
  return null;
}
