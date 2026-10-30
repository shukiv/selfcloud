// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import LangSwitcher, { change } from "./LangSwitcher";

function renderSwitcher() {
  return render(
    <ThemeProvider>
      <Direction>
        <LangSwitcher />
      </Direction>
    </ThemeProvider>,
  );
}

function signedIn(): Session {
  return {
    user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  };
}

// pick opens the menu and clicks the named language in it.
async function pick(name: string) {
  fireEvent.click(screen.getByTestId("language"));
  fireEvent.click(await screen.findByRole("menuitem", { name }));
}

describe("LangSwitcher", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    session.set(null);
    window.localStorage.clear();
    await setLang("en");
  });

  it("switches at once from the menu and, signed out, saves the choice in this browser", async () => {
    session.set(null);
    renderSwitcher();
    await pick("עברית");
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    expect(document.documentElement.lang).toBe("he");
    await waitFor(() => expect(window.localStorage.getItem("selfcloud.lang")).toBe("he"));
  });

  // At phone width the top bar has no room for the language's name: the control is the icon, and its name stays "Language"
  // (guard: the compact prop, which the top bar passes at phone width).
  it("at phone width shows the icon only and keeps its accessible name", () => {
    render(
      <ThemeProvider>
        <Direction>
          <LangSwitcher compact />
        </Direction>
      </ThemeProvider>,
    );
    const control = screen.getByRole("button", { name: "Language" });
    expect(control.getAttribute("data-testid")).toBe("language");
    expect(control.textContent?.trim()).toBe("");
  });

  // The choice is called directly here: reopening the menu in one test races the popup's closing animation.
  it("saves the choice to the account when signed in", async () => {
    session.set(signedIn());
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ user: { locale: "he" } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    await change("he");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(String(url)).toContain("/api/v1/me");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(String(init.body))).toEqual({ locale: "he" });
  });
});
