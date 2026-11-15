// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { goSignedOut, outsideSignIn, sendToSignIn, setSignInInfo, SIGNED_OUT_PATH } from "./outsideSignIn";
import { stubLocationAssign } from "../test/locationStub";

const OUTSIDE = "https://id.example.com/sign-in?next={next}";

function setOutside(signedOutUrl = ""): void {
  setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl, password: false });
}

beforeEach(() => {
  setSignInInfo(null);
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/");
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  setSignInInfo(null);
  window.sessionStorage.clear();
});

describe("outsideSignIn", () => {
  it("is null when the installation names no outside page", () => {
    expect(outsideSignIn("/files")).toBeNull();
  });

  it("replaces {next} with the encoded path", () => {
    setOutside();
    expect(outsideSignIn("/files/abc")).toBe("https://id.example.com/sign-in?next=%2Ffiles%2Fabc");
  });

  it("encodes a query in the path", () => {
    setOutside();
    expect(outsideSignIn("/search?q=a&b=c")).toBe("https://id.example.com/sign-in?next=%2Fsearch%3Fq%3Da%26b%3Dc");
  });

  it("sends an unsafe next to the home path", () => {
    setOutside();
    for (const raw of ["//evil.example/x", "https://evil.example/x", "/\\evil.example", "/.//evil.example", "", "/a b"]) {
      expect(outsideSignIn(raw)).toBe("https://id.example.com/sign-in?next=%2F");
    }
  });

  it("leaves a URL without the placeholder as it is", () => {
    setSignInInfo({ externalUrl: "https://id.example.com/sign-in", signedOutUrl: "", password: false });
    expect(outsideSignIn("/files")).toBe("https://id.example.com/sign-in");
  });
});

describe("sendToSignIn", () => {
  it("goes to the app's sign-in page when no outside page is set", () => {
    const assign = stubLocationAssign();
    sendToSignIn("/files/abc?x=1");
    expect(assign).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe("/login");
    expect(window.location.search).toBe("?next=%2Ffiles%2Fabc%3Fx%3D1");
  });

  it("assigns the outside page when one is set", () => {
    setOutside();
    const assign = stubLocationAssign();
    sendToSignIn("/files/abc");
    expect(assign).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2Ffiles%2Fabc");
    expect(window.location.pathname).toBe("/");
  });

  it("shows the signed-out page on the third redirect within a minute", () => {
    setOutside();
    const assign = stubLocationAssign();
    sendToSignIn("/files/a");
    sendToSignIn("/files/b");
    sendToSignIn("/files/c");
    expect(assign).toHaveBeenCalledTimes(2);
    expect(window.location.pathname).toBe(SIGNED_OUT_PATH);
  });

  it("lets a redirect go again once the minute has passed", () => {
    vi.useFakeTimers();
    setOutside();
    const assign = stubLocationAssign();
    sendToSignIn("/files/a");
    sendToSignIn("/files/b");
    vi.advanceTimersByTime(61_000);
    sendToSignIn("/files/c");
    expect(assign).toHaveBeenCalledTimes(3);
    expect(window.location.pathname).toBe("/");
  });

  it("still redirects when the tab's storage cannot be read", () => {
    setOutside();
    const assign = stubLocationAssign();
    // jsdom's storage proxy does not let a method be spied on, so the whole store refuses here, as a browser's can.
    const real = Object.getOwnPropertyDescriptor(window, "sessionStorage");
    Object.defineProperty(window, "sessionStorage", {
      configurable: true,
      get() {
        throw new Error("refused");
      },
    });
    try {
      sendToSignIn("/files/a");
      sendToSignIn("/files/b");
      sendToSignIn("/files/c");
      expect(assign).toHaveBeenCalledTimes(3);
    } finally {
      if (real) Object.defineProperty(window, "sessionStorage", real);
    }
  });
});

describe("goSignedOut", () => {
  it("assigns the signed-out URL when one is set", () => {
    setOutside("https://id.example.com/signed-out");
    const assign = stubLocationAssign();
    goSignedOut();
    expect(assign).toHaveBeenCalledWith("https://id.example.com/signed-out");
  });

  it("shows the signed-out page when only the outside sign-in page is set", () => {
    setOutside();
    const assign = stubLocationAssign();
    goSignedOut();
    expect(assign).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe(SIGNED_OUT_PATH);
  });

  it("goes to /login when neither is set", () => {
    const assign = stubLocationAssign();
    goSignedOut();
    expect(assign).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe("/login");
  });
});