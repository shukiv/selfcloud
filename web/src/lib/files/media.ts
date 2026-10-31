// SPDX-License-Identifier: AGPL-3.0-or-later

// The decisions a video or audio player makes when the browser reports a failure, kept apart from the element so they
// can be tested. A signed link ends at its expiry, and the server refuses every request for it after that, including
// the Range requests a player makes when someone seeks. So a failure after the link's end is answered with a new link,
// and the player resumes at the same place. The decision reads no clock: the server's expiry and the browser's clock
// may disagree, so only what the player saw counts (did playback move on, and which link is this).

// The codes of MediaError, as the browser reports them.
export const MEDIA_ERR_NETWORK = 2;
export const MEDIA_ERR_DECODE = 3;
export const MEDIA_ERR_SRC_NOT_SUPPORTED = 4;

// MAX_RENEWALS is how many new links a player asks for in a row when playback has not moved on, for stalls. A failure
// needs no such count: a fresh link that fails before playback moves on is never renewed again.
export const MAX_RENEWALS = 3;

// PROGRESS_SECONDS is how far playback must move past a place for it to count as progress.
export const PROGRESS_SECONDS = 1;

export type MediaVerdict = "renew" | "unsupported" | "error";

// mediaVerdict says what a media error means, from three facts the player has: the code; whether playback moved on past
// the place the current link began at (progressed); whether the current link is one asked for after a failure (renewed);
// and whether the renewal count allows one more link (mayRenew).
// A link that failed before playback moved on may have ended, or may be unplayable: one renewal settles which. A fresh
// link that fails the same way before playback moves on is the cause, so a decode or source failure is a format the
// browser cannot read, and a network failure is an error. A link that ended after playback moved on is renewed.
export function mediaVerdict(code: number | null, progressed: boolean, renewed: boolean, mayRenew: boolean): MediaVerdict {
  if (renewed && !progressed) return code === MEDIA_ERR_DECODE || code === MEDIA_ERR_SRC_NOT_SUPPORTED ? "unsupported" : "error";
  if (mayRenew) return "renew";
  return "error";
}

// Renewals counts the links asked for since playback last moved on, and where playback was at the last renewal.
export interface Renewals {
  failures: number;
  at: number;
}

export const NO_RENEWALS: Renewals = { failures: 0, at: -Infinity };

// afterProgress starts the count again once playback is past the place of the last renewal.
export function afterProgress(r: Renewals, currentTime: number): Renewals {
  return currentTime > r.at + PROGRESS_SECONDS && r.failures > 0 ? NO_RENEWALS : r;
}

// takeRenewal says whether one more link may be asked for, and the count after asking. A renewal made at the
// playback time given is counted there.
export function takeRenewal(r: Renewals, currentTime: number): { allowed: boolean; next: Renewals } {
  if (r.failures >= MAX_RENEWALS) return { allowed: false, next: r };
  return { allowed: true, next: { failures: r.failures + 1, at: currentTime } };
}

// Gate says which answer is the current one. A player asks for a link, the person may move on before the answer comes,
// and an answer for an earlier request must not reach the element.
export class Gate {
  private latest = 0;

  // begin starts a request and returns its number.
  begin(): number {
    return ++this.latest;
  }

  // current says whether the answer to a request is still wanted.
  current(id: number): boolean {
    return id === this.latest;
  }

  // cancel makes every request in flight stale: the person closed the player, or moved to another file.
  cancel(): void {
    this.latest++;
  }
}
