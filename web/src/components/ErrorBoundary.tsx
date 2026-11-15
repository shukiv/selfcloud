// SPDX-License-Identifier: AGPL-3.0-or-later

import { Component, lazy, Suspense, type ReactNode } from "react";
import SectionBoundary from "./SectionBoundary";

// The crash view is a lazy chunk (decision 185): antd's Result and its pictures are not on the first load's path.
// The entry warms it once the first screen is drawn (main.tsx), so a crash finds it already there. A chunk that
// cannot load (an open tab after a deploy has new file names) leaves the section's own message and reload, not a
// blank page: the boundary around it keeps the failure to this place.
const CrashView = lazy(() => import("./CrashView"));

// ErrorBoundary is the last line of the page: a render error shows a message and a reload button, and nothing
// else of the failed tree. On the share-link address the secret is in the fragment, so the fragment is stripped
// first (the page reads the secret once, before any render that could fail, so nothing is lost by it). The error
// itself is not shown, and this code keeps and sends nothing of it. React 18 still reports a caught render error
// through console.error (in production too), so the console copy is React's, not ours.
export default class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(): void {
    if (window.location.pathname === "/s" && window.location.hash !== "") {
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  }

  render(): ReactNode {
    return this.state.failed ? (
      <SectionBoundary>
        <Suspense fallback={null}>
          <CrashView />
        </Suspense>
      </SectionBoundary>
    ) : (
      this.props.children
    );
  }
}
