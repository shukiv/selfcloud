// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import AuthLayout from "./AuthLayout";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { setLang } from "../lib/i18n";
import { brand, brandLoaded, DEFAULT_ACCENT } from "../lib/brand";

// The sign-in brand (decision 109): the SelfCloud artwork is one picture with the name inside it, so the name is not drawn beside it.
// A custom logo or name is drawn as before. Nothing is drawn before the brand is loaded.
function renderAuth() {
  return render(
    <ThemeProvider>
      <Direction>
        <AuthLayout>
          <p>form</p>
        </AuthLayout>
      </Direction>
    </ThemeProvider>,
  );
}

describe("AuthLayout brand (decision 109)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    brandLoaded.set(true);
  });
  afterEach(() => {
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
  });

  it("shows the artwork alone, from /brand/logo, and no name beside it", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderAuth();
    const imgs = container.querySelectorAll("img");
    expect(imgs.length).toBeGreaterThan(0);
    for (const img of imgs) expect(img.getAttribute("src")).toBe("/brand/logo");
    expect(container.querySelector(".auth-brand-name")).toBeNull();
  });

  // Decision 126: a custom name with no logo file gets the artwork too, with no name beside it.
  it("shows the artwork for a custom name with the artwork, and no name beside it", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: true });
    const { container } = renderAuth();
    const img = container.querySelector("img");
    expect(img?.getAttribute("src")).toBe("/brand/logo");
    expect(img?.getAttribute("alt")).toBe("Acme Drive");
    expect(container.querySelector(".auth-brand-name")).toBeNull();
  });

  it("shows a custom name as text and no picture", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const { container } = renderAuth();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".auth-brand-name")?.textContent).toBe("Acme Drive");
  });

  it("shows a custom logo with the name, as before", () => {
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: false });
    const { container } = renderAuth();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("/brand/logo");
    expect(container.querySelector(".auth-brand-name")?.textContent).toBe("Acme Drive");
  });

  // Before the server's answer there is no name and no picture, and the logo's space stays, so the card does not move.
  it("shows no name and no picture before the brand is loaded", () => {
    brandLoaded.set(false);
    brand.set({ name: "", accent: DEFAULT_ACCENT, logo_url: null, artwork: false });
    const { container } = renderAuth();
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector(".auth-brand-name")).toBeNull();
    expect(container.textContent).not.toContain("SelfCloud");
  });
});
