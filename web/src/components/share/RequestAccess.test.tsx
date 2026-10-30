// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";

const api = vi.hoisted(() => ({ createAccessRequest: vi.fn() }));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import RequestAccess from "./RequestAccess";

describe("request access", () => {
  beforeEach(() => api.createAccessRequest.mockResolvedValue(undefined));
  afterEach(() => vi.clearAllMocks());

  it("sends the request and gives the one confirmation, whatever the item", async () => {
    const user = userEvent.setup();
    render(<RequestAccess nodeID="nod_secret" />);
    await user.type(document.getElementById("request-message") as HTMLElement, "Need it for the review");
    await user.click(screen.getByRole("button", { name: "Ask for access" }));
    expect(await screen.findByText("If this item exists, the people who manage it have been asked.")).toBeTruthy();
    expect(api.createAccessRequest).toHaveBeenCalledWith("nod_secret", "viewer", "Need it for the review");
  });

  it("a rate limit is said apart, in words for its code, and the server text is never shown", async () => {
    const user = userEvent.setup();
    api.createAccessRequest.mockRejectedValueOnce(new ApiError(429, "rate_limited", {}, 120));
    render(<RequestAccess nodeID="nod_secret" />);
    await user.click(screen.getByRole("button", { name: "Ask for access" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("SERVER TEXT")).toBeNull();
  });
});
