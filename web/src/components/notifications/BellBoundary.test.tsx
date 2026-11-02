// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import BellBoundary from "./BellBoundary";

// A bell that cannot load (its chunk is gone after a deploy) throws where it renders. The boundary draws nothing in its
// place and leaves the rest of the top bar alone; the page around it is not replaced.
function Broken(): never {
  throw new Error("chunk did not load");
}

describe("the bell's boundary", () => {
  beforeEach(() => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("draws nothing where a failed bell was, and keeps the rest of the bar", () => {
    render(
      <div>
        <span>bar</span>
        <BellBoundary>
          <Broken />
        </BellBoundary>
        <span>after</span>
      </div>,
    );
    expect(screen.getByText("bar")).toBeTruthy();
    expect(screen.getByText("after")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows a working bell as it is", () => {
    render(
      <BellBoundary>
        <span>working</span>
      </BellBoundary>,
    );
    expect(screen.getByText("working")).toBeTruthy();
  });
});
