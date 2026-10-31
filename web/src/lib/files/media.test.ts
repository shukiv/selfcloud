// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { Gate, MAX_RENEWALS, NO_RENEWALS, afterProgress, mediaVerdict, takeRenewal } from "./media";

// mediaVerdict never reads a clock: the server's expiry is not trusted to match the browser's, so the decision is made from
// what the player saw, and from how far playback moved past the place the current link began at.
describe("mediaVerdict: the error mapping, from playback alone", () => {
  it("renews a link that failed before playback moved on, whatever the code", () => {
    expect(mediaVerdict(2, false, false, true)).toBe("renew");
    expect(mediaVerdict(3, false, false, true)).toBe("renew");
    expect(mediaVerdict(4, false, false, true)).toBe("renew");
    expect(mediaVerdict(null, false, false, true)).toBe("renew");
  });

  it("renews a link that ended partway through playback, so the player resumes", () => {
    expect(mediaVerdict(4, true, false, true)).toBe("renew");
    expect(mediaVerdict(2, true, true, true)).toBe("renew");
  });

  it("gives an error, and asks for nothing more, once the renewal count is used up", () => {
    expect(mediaVerdict(2, true, false, false)).toBe("error");
    expect(mediaVerdict(4, true, true, false)).toBe("error");
  });

  it("says a fresh link that fails the same way, before playback moved on, is a format the browser cannot read", () => {
    expect(mediaVerdict(3, false, true, false)).toBe("unsupported");
    expect(mediaVerdict(4, false, true, false)).toBe("unsupported");
    expect(mediaVerdict(3, false, true, true)).toBe("unsupported");
  });

  it("says a fresh link that fails with a network error is an error, not a format", () => {
    expect(mediaVerdict(2, false, true, true)).toBe("error");
    expect(mediaVerdict(null, false, true, true)).toBe("error");
  });
});

describe("takeRenewal: the bounded retry", () => {
  it("allows MAX_RENEWALS links in a row, then refuses", () => {
    let r = NO_RENEWALS;
    for (let i = 0; i < MAX_RENEWALS; i++) {
      const got = takeRenewal(r, 10);
      expect(got.allowed).toBe(true);
      r = got.next;
    }
    expect(takeRenewal(r, 10).allowed).toBe(false);
    expect(takeRenewal(r, 10).next).toBe(r);
  });

  it("starts the count again once playback is past the last renewal", () => {
    let r = NO_RENEWALS;
    for (let i = 0; i < MAX_RENEWALS; i++) r = takeRenewal(r, 100).next;
    expect(takeRenewal(r, 100).allowed).toBe(false);
    // Still at the same place: no progress, so still refused.
    expect(takeRenewal(afterProgress(r, 100.5), 100).allowed).toBe(false);
    // Past it: a long video that keeps playing can renew again, as often as it needs to.
    const moved = afterProgress(r, 103);
    expect(moved.failures).toBe(0);
    expect(takeRenewal(moved, 103).allowed).toBe(true);
  });

  it("does not reset a count that has not moved", () => {
    const r = takeRenewal(NO_RENEWALS, 5).next;
    expect(afterProgress(r, 5)).toBe(r);
  });
});

describe("Gate: the stale-answer guard", () => {
  it("accepts only the newest request", () => {
    const g = new Gate();
    const first = g.begin();
    const second = g.begin();
    expect(g.current(first)).toBe(false);
    expect(g.current(second)).toBe(true);
  });

  it("makes a request in flight stale when the player is closed or moves on", () => {
    const g = new Gate();
    const asked = g.begin();
    g.cancel();
    expect(g.current(asked)).toBe(false);
  });
});
