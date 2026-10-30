// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { FolderAccess } from "../../lib/files/types";

const api = vi.hoisted(() => ({ updateSpaceSettings: vi.fn() }));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import SpaceSettings from "./SpaceSettings";

const TEAM: FolderAccess = {
  id: "fld_team",
  name: "Team",
  kind: "team",
  access: "write",
  role: "manager",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
};

describe("space settings", () => {
  beforeEach(() => api.updateSpaceSettings.mockResolvedValue({}));
  afterEach(() => vi.clearAllMocks());

  it("saves a switch when it changes", async () => {
    const user = userEvent.setup();
    const onsaved = vi.fn();
    render(<SpaceSettings space={TEAM} onclose={vi.fn()} onsaved={onsaved} />);
    await user.click(screen.getAllByRole("switch")[0]);
    await waitFor(() => expect(api.updateSpaceSettings).toHaveBeenCalledWith("fld_team", { members_only: true }));
    expect(await screen.findByText("Saved.")).toBeTruthy();
    expect(onsaved).toHaveBeenCalledWith(expect.objectContaining({ members_only: true }));
  });

  it("a refused change puts the switch back and says why by code", async () => {
    const user = userEvent.setup();
    api.updateSpaceSettings.mockRejectedValueOnce(new ApiError(403, "forbidden", {}, 0));
    render(<SpaceSettings space={TEAM} onclose={vi.fn()} onsaved={vi.fn()} />);
    const sw = screen.getAllByRole("switch")[0];
    await user.click(sw);
    expect(await screen.findByText("Only a manager of this space can change its settings.")).toBeTruthy();
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("a click on the mask does not close it", async () => {
    const onclose = vi.fn();
    render(<SpaceSettings space={TEAM} onclose={onclose} onsaved={vi.fn()} />);
    const wrap = document.querySelector(".ant-modal-wrap") as HTMLElement;
    wrap.click();
    expect(onclose).not.toHaveBeenCalled();
  });
});
