// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame's session-ended view (decision 214): it draws the embed catalogue's words in the shared auth
// layout, tells the host once per remembered origin, and offers no way to sign in. While the catalogue is on
// its way it draws a Spin; a failed load shows the main catalogue's error with a Retry that loads it again.

import { StrictMode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const frGate = vi.hoisted(() => {
  let release = () => {};
  const promise = new Promise<void>((r) => {
    release = r;
  });
  return { promise, release: () => release() };
});
vi.mock("../../i18n/embed/fr.json", async () => {
  await frGate.promise;
  return await vi.importActual<{ default: Record<string, string> }>("../../i18n/embed/fr.json");
});
const heFail = vi.hoisted(() => ({ on: true }));
vi.mock("../../i18n/embed/he.json", async () => {
  if (heFail.on) throw new Error("chunk failed");
  return await vi.importActual<{ default: Record<string, string> }>("../../i18n/embed/he.json");
});

import heMain from "../../i18n/he.json";
import { setLang } from "../../lib/i18n";
import { rememberFrame } from "../../lib/frame";
import { ThemeProvider } from "../../theme/theme";
import FrameEnded from "./FrameEnded";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;
const ORIGINS = ["https://host.example", "https://other.example"];

function draw() {
  return render(
    <ThemeProvider>
      <StrictMode>
        <FrameEnded />
      </StrictMode>
    </ThemeProvider>,
  );
}

beforeEach(() => {
  window.sessionStorage.clear();
  Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
  rememberFrame(ORIGINS);
});

afterEach(async () => {
  Object.defineProperty(window, "self", SELF);
  delete (window.location as Location & { ancestorOrigins?: unknown }).ancestorOrigins;
  delete (document as Document & { referrer?: string }).referrer;
  vi.restoreAllMocks();
  window.sessionStorage.clear();
  await setLang("en");
});

describe("FrameEnded", () => {
  it("draws the title and the host's name, and tells the host once per origin", async () => {
    Object.defineProperty(document, "referrer", { value: "https://host.example/app", configurable: true });
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    const { container } = draw();
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(container.textContent).toContain("Open the drive again from host.example");
    expect(post).toHaveBeenCalledTimes(2);
    expect(post).toHaveBeenNthCalledWith(1, { type: "selfcloud.session-ended" }, ORIGINS[0]);
    expect(post).toHaveBeenNthCalledWith(2, { type: "selfcloud.session-ended" }, ORIGINS[1]);
    for (const call of post.mock.calls) expect(call[1]).not.toBe("*");
  });

  it("offers no password form and no forgot-password way", async () => {
    const { container } = draw();
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(container.querySelector('input[type="password"]')).toBeNull();
    expect(screen.queryByText("Forgot password")).toBeNull();
  });

  it("names the application it is in when the host cannot be told", async () => {
    const { container } = draw();
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(container.textContent).toContain("Open the drive again from the application it is in");
  });

  it("does not name a parent whose origin is not in the list", async () => {
    Object.defineProperty(document, "referrer", { value: "https://stranger.example/app", configurable: true });
    const { container } = draw();
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(container.textContent).toContain("Open the drive again from the application it is in");
    expect(container.textContent).not.toContain("stranger.example");
  });

  it("draws a Spin until the catalogue is there, then the translated words", async () => {
    await setLang("fr");
    const { container } = draw();
    expect(container.querySelector(".ant-spin")).not.toBeNull();
    expect(container.textContent).not.toContain("Your session ended");
    frGate.release();
    expect(await screen.findByText(/Votre session/)).toBeTruthy();
  });

  it("a failed load shows the error with Retry, and Retry loads the catalogue again", async () => {
    await setLang("he");
    draw();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain(heMain["app.crash.title"]);
    heFail.on = false;
    fireEvent.click(screen.getByRole("button", { name: heMain["upload.retry"] }));
    expect(await screen.findByText(/ההתחברות/)).toBeTruthy();
  });
});
