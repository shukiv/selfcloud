// SPDX-License-Identifier: AGPL-3.0-or-later

import { lazy, Suspense, useEffect, useSyncExternalStore } from "react";
import { getPlayback, stopPlayback, subscribePlayback } from "../../lib/files/playback";

// MediaHost is the shell's place for the media session. It is always mounted in the shell and draws nothing without a
// session; the player and its viewer load when a session starts, so they stay out of the first load. Leaving the shell
// (a sign-out, or a page outside it) ends the session: nothing plays on behind the next screen.
const MediaDock = lazy(() => import("./MediaDock"));

export default function MediaHost() {
  const snap = useSyncExternalStore(subscribePlayback, getPlayback, getPlayback);
  useEffect(() => () => stopPlayback(), []);
  if (snap.seed === null) return null;
  return (
    <Suspense fallback={null}>
      <MediaDock key={snap.id} />
    </Suspense>
  );
}
