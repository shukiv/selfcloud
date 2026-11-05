// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { session, type Session } from "../session";
import { clearOnSessionChange, createQueryClient } from "../query";
import { resetUserStores } from "../store";
import { closedFor, holdClose, startPlace } from "./treeHold";

// The session a sign-in makes (setSignedIn in lib/session.ts), with the person's id and token.
function signedIn(userID: string, csrf: string): Session {
  return {
    user: { id: userID, email: `${userID}@example.com`, display_name: userID, role: "member", locale: "en", status: "active" },
    account: null,
    csrf,
    publicLinks: false,
  } as unknown as Session;
}

describe("the tree's hold of a hand close (decision 88)", () => {
  let stop = () => {};
  afterEach(() => {
    stop();
    session.set(null);
    resetUserStores();
  });

  // Viewing as another person (lib/impersonation.ts, adoptSession) and going back both end in setSignedIn or setMe, which set a session
  // with the other person's id. The session key changes, clearOnSessionChange (the wiring in main.tsx) empties the user's stores, and the
  // hold goes with them.
  it("a hand close holds for the person who made it, and resets when the viewed person changes", () => {
    session.set(signedIn("u1", "t1"));
    stop = clearOnSessionChange(createQueryClient());
    startPlace("fld_a|nod_one");
    holdClose("fld_a|nod_one", "nod_one");
    expect(closedFor("fld_a|nod_one")).toEqual(["nod_one"]);
    session.set(signedIn("tenant_user", "t2"));
    expect(closedFor("fld_a|nod_one")).toEqual([]);
  });

  it("a sign-out and a sign-in as the same person, with a new token, also reset the hold", () => {
    session.set(signedIn("u1", "t1"));
    stop = clearOnSessionChange(createQueryClient());
    startPlace("fld_a|nod_one");
    holdClose("fld_a|nod_one", "nod_one");
    session.set(null);
    session.set(signedIn("u1", "t3"));
    expect(closedFor("fld_a|nod_one")).toEqual([]);
  });
});
