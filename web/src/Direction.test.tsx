// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { setLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";
import { Direction } from "./App";

describe("Direction", () => {
  beforeEach(async () => {
    await setLang("en");
  });

  // A remount loses what the page holds: text typed into a form, an open menu, focus.
  it("keeps the page mounted while antd's locale loads and when the language changes", async () => {
    let mounts = 0;
    function Page() {
      useEffect(() => {
        mounts++;
      }, []);
      return <p>page</p>;
    }
    render(
      <ThemeProvider>
        <Direction>
          <Page />
        </Direction>
      </ThemeProvider>,
    );
    await screen.findByText("page");
    // Long enough for the locale chunk to arrive.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 500));
    });
    expect(mounts).toBe(1);
    await act(async () => {
      await setLang("he");
    });
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 500));
    });
    expect(mounts).toBe(1);
  });
});
