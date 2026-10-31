// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FolderAccess } from "../../lib/files/types";
import PickDestination from "./PickDestination";

const space: FolderAccess = {
  id: "fld_1",
  name: "Team",
  kind: "team",
  access: "write",
  role: "contributor",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
};

describe("PickDestination for a shortcut", () => {
  it("does not offer a space's root: a shortcut goes in a directory", () => {
    const onPick = vi.fn();
    render(<PickDestination mode="shortcut" spaces={[space]} onPick={onPick} onClose={vi.fn()} />);
    expect(screen.getByRole("dialog").textContent).toContain("Add shortcut to");
    fireEvent.click(screen.getByText("Team"));
    const next = screen.getByRole("button", { name: "Next" }) as HTMLButtonElement;
    expect(next.disabled).toBe(true);
    fireEvent.click(next);
    expect(onPick).not.toHaveBeenCalled();
  });
});
