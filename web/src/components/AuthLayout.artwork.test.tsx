// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import AuthLayout from "./AuthLayout";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { setLang } from "../lib/i18n";
import { brand, DEFAULT_ACCENT } from "../lib/brand";

// The sign-in brand (decision 109): the SelfCloud artwork is one picture with the name inside it, so the name is not drawn beside
// it. A custom logo or name is drawn as before.
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
  });
  afterEach(() => brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null }));

  it("shows the artwork alone, and no name beside it", () => {
    brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: "/brand/logo", artwork: true });
    const { container } = renderAuth();
    const imgs = container.querySelectorAll("img");
    expect(imgs.length).toBeGreaterThan(0);
    for (const img of imgs) expect(img.getAttribute("src")).toBe("/brand/logo");
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
});
