// SPDX-License-Identifier: AGPL-3.0-or-later

// The approval page's strings are a catalogue of their own (web/src/i18n/cli). This file is the one that runs the lazy path: it
// does not preload the catalogue, and it is the first test in its file, so the first render has nothing loaded. The page must
// show no key and no English before the catalogue arrives (a spinner), then its real strings. It stays in its own file so that
// the other tests, which preload the catalogue, do not hide the path.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import CliApprovePage from "./CliApprovePage";
import { ThemeProvider } from "../theme/theme";

describe("the approval page loads its own strings", () => {
  it("shows a spinner, no key and no English before the catalogue arrives, then its real strings", async () => {
    const { container } = render(
      <ThemeProvider>
        <CliApprovePage />
      </ThemeProvider>,
    );
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
    expect(screen.queryByText("Approve a terminal sign-in")).toBeNull();
    expect(container.querySelector(".ant-spin")).not.toBeNull();
    expect(await screen.findByText("Approve a terminal sign-in")).toBeTruthy();
    expect(screen.getByLabelText("Code from the terminal")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/cliApprove\.|devices\./);
  });
});
