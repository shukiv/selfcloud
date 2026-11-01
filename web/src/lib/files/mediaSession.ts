// SPDX-License-Identifier: AGPL-3.0-or-later

// mediaSession ties the playing file to the browser's media controls: the keys of a keyboard or headset, and the lock
// screen and notification controls on a phone. It sets the title and the actions while a file is playing, and removes
// them when the playback stops, so a control never reaches a file that is no longer there. A browser without the API
// (or one that refuses an action) gets no controls, and the player works without them.

export interface SessionHandlers {
  play: () => void;
  pause: () => void;
  previous: () => void;
  next: () => void;
  seekBy: (seconds: number) => void;
  seekTo: (seconds: number) => void;
}

// SEEK_S is how far the seek actions move: 10 seconds, as the player's J and L keys do.
export const SEEK_S = 10;

// session returns the browser's Media Session, or null when the browser has none.
function session(): MediaSession | null {
  if (typeof navigator === "undefined" || !("mediaSession" in navigator)) return null;
  return navigator.mediaSession ?? null;
}

// bindMediaSession shows the title and routes the controls to h. It returns the function that removes them again. Each
// action is set on its own: an action the browser refuses (it throws) is left out, and the others still work.
export function bindMediaSession(title: string, h: SessionHandlers): () => void {
  const ms = session();
  if (ms === null) return () => undefined;
  if (typeof MediaMetadata !== "undefined") {
    try {
      ms.metadata = new MediaMetadata({ title });
    } catch {
      // A title the browser will not take is left unset; the controls still work.
    }
  }
  const actions: Array<[MediaSessionAction, MediaSessionActionHandler]> = [
    ["play", () => h.play()],
    ["pause", () => h.pause()],
    ["previoustrack", () => h.previous()],
    ["nexttrack", () => h.next()],
    ["seekbackward", () => h.seekBy(-SEEK_S)],
    ["seekforward", () => h.seekBy(SEEK_S)],
    ["seekto", (d) => {
      if (typeof d.seekTime === "number" && Number.isFinite(d.seekTime)) h.seekTo(d.seekTime);
    }],
  ];
  for (const [action, fn] of actions) {
    try {
      ms.setActionHandler(action, fn);
    } catch {
      // This browser does not take this action: the others are still set.
    }
  }
  return () => {
    for (const [action] of actions) {
      try {
        ms.setActionHandler(action, null);
      } catch {
        // Already unset by the browser.
      }
    }
    ms.metadata = null;
    // Nothing plays on under this session any more: the lock screen shows no state.
    ms.playbackState = "none";
  };
}

// setPlaybackState tells the browser whether the file plays, and where it is, so the lock screen shows the right state.
export function setPlaybackState(playing: boolean, position: number, duration: number): void {
  const ms = session();
  if (ms === null) return;
  ms.playbackState = playing ? "playing" : "paused";
  if (duration > 0 && Number.isFinite(duration) && position >= 0 && position <= duration) {
    try {
      ms.setPositionState({ duration, position, playbackRate: 1 });
    } catch {
      // A position the browser refuses is not shown; the controls still work.
    }
  }
}
