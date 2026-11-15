// SPDX-License-Identifier: AGPL-3.0-or-later

// A session the server ended while the installation names an outside sign-in page (decision 204): the 401 sends the
// browser to that page with the path it was on, and a third such redirect within a minute shows the signed-out page
// instead, so a sign-in that never lands cannot loop.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { session } from "./session";
import { location } from "./router";
import { setSignInInfo, SIGNED_OUT_PATH } from "./outsideSignIn";
import { stubLocationAssign } from "../test/locationStub";

const OUTSIDE = "https://id.example.com/sign-in?next={next}";

function signedIn(): void {
  session.set({ user: { id: "usr_a" } as never, account: null, csrf: "tok", publicLinks: false });
}

let assign: ReturnType<typeof stubLocationAssign>;

beforeEach(() => {
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/files/abc");
  location.set({ path: "/files/abc", search: "" });
  setSignInInfo(null);
  signedIn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ error: { code: "unauthenticated" } }), { status: 401, headers: { "content-type": "application/json" } })),
  );
  assign = stubLocationAssign();
});

afterEach(() => {
  vi.unstubAllGlobals();
  session.set(null);
  setSignInInfo(null);
  window.sessionStorage.clear();
});

describe("a 401 with an outside sign-in page", () => {
  it("assigns the outside page with the path the person was on", async () => {
    setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl: "", password: false });
    await expect(api("GET", "/api/v1/nodes/folder")).rejects.toBeDefined();
    expect(assign).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2Ffiles%2Fabc");
  });

  it("shows the signed-out page on the third redirect within a minute", async () => {
    setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl: "", password: false });
    for (let i = 0; i < 3; i++) {
      signedIn();
      await expect(api("GET", "/api/v1/nodes/folder")).rejects.toBeDefined();
    }
    expect(assign).toHaveBeenCalledTimes(2);
    expect(window.location.pathname).toBe(SIGNED_OUT_PATH);
  });

  it("goes to /login as before when no outside page is set", async () => {
    await expect(api("GET", "/api/v1/nodes/folder")).rejects.toBeDefined();
    expect(assign).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe("/login");
  });
});