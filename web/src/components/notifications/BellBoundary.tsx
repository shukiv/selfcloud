// SPDX-License-Identifier: AGPL-3.0-or-later

import { Component, type ReactNode } from "react";

// BellBoundary keeps a failed bell to its own place: when its chunk does not load (an open tab after a deploy has new
// file names) or it throws, the top bar draws nothing where the bell was, and the rest of the page stays. The page's own
// error boundary would replace the whole app, which is too much for a convenience.
export default class BellBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  render(): ReactNode {
    return this.state.failed ? null : this.props.children;
  }
}
