// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { session, type Session } from "../../lib/session";
import { maxExpiryDays, dateInputValue } from "../../lib/sharing/limits";
import { latestAnyoneEnd } from "../../lib/sharing/anyone";
import { passwordMinLength } from "../../lib/brand";
import type { Node, Role } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  listShareLinks: vi.fn(),
  createShareLink: vi.fn(),
  updateShareLink: vi.fn(),
  deleteShareLink: vi.fn(),
  resetShareLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import GeneralAccess from "./GeneralAccess";

const SECRET_URL = "https://drive.example/s#q7vY2nXh0eJkLp9RrTbWcFgA3sDmNuZoIyUlPaKd8Qs";

function person(publicLinks: boolean): Session {
  return {
    user: { id: "usr_ann", email: "ann@example.com", display_name: "Ann", role: "member", locale: "en" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-ann",
    publicLinks,
  } as unknown as Session;
}

function nodeOf(role: Role): Node {
  return {
    id: "nod_board",
    folder_id: "fld_team",
    parent_id: null,
    name: "Board",
    kind: "dir",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
  };
}

function renderAccess(role: Role = "manager", cap: Role | null = role) {
  const client = createQueryClient();
  const onchanged = vi.fn();
  const view = render(
    <QueryClientProvider client={client}>
      <GeneralAccess node={nodeOf(role)} cap={cap} onchanged={onchanged} onopen={vi.fn()} />
    </QueryClientProvider>,
  );
  return { ...view, onchanged, client };
}

describe("general access", () => {
  beforeEach(() => {
    session.set(person(true));
    maxExpiryDays.set(0);
    passwordMinLength.set(8);
    api.listShareLinks.mockResolvedValue({ items: [] });
    api.createShareLink.mockResolvedValue({
      link: { id: "shl_1", node_id: "nod_board", audience: "anyone", role: "viewer", expires_at: null, created_at: "", inherited_from: null },
      url: SECRET_URL,
    });
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
    sessionStorage.clear();
    localStorage.clear();
  });

  it("the link URL is shown once, after it is made, and never again on a later visit", async () => {
    const user = userEvent.setup();
    const first = renderAccess();
    await user.click(await screen.findByLabelText("Anyone in Acme"));
    await user.click(await screen.findByRole("button", { name: "Create link" }));
    expect(await screen.findByDisplayValue(SECRET_URL)).toBeTruthy();
    first.unmount();

    api.listShareLinks.mockResolvedValue({
      items: [{ id: "shl_1", node_id: "nod_board", audience: "anyone", role: "viewer", expires_at: null, created_at: "", inherited_from: null, password_set: false }],
    });
    renderAccess();
    await screen.findByLabelText("Anyone in Acme");
    expect(screen.queryByDisplayValue(SECRET_URL)).toBeNull();
  });

  it("the secret is never written to storage", async () => {
    const user = userEvent.setup();
    renderAccess();
    await user.click(await screen.findByLabelText("Anyone in Acme"));
    await user.click(await screen.findByRole("button", { name: "Create link" }));
    await screen.findByDisplayValue(SECRET_URL);
    expect(sessionStorage.length + localStorage.length).toBe(0);
    expect(api.createShareLink).toHaveBeenCalledWith("nod_board", expect.not.objectContaining({ url: expect.anything() }));
  });

  it("an anyone link refuses a password under the minimum, and sends nothing", async () => {
    const user = userEvent.setup();
    renderAccess();
    await user.click(await screen.findByLabelText("Anyone with the link"));
    await user.type(document.getElementById("share-anyone-password") as HTMLElement, "abc");
    await user.click(screen.getByRole("button", { name: "Create link" }));
    expect(await screen.findByText("The password must be at least 8 characters.")).toBeTruthy();
    expect(api.createShareLink).not.toHaveBeenCalled();
  });

  it("the end date of an anyone link stops at the sharing cap", async () => {
    maxExpiryDays.set(10);
    const user = userEvent.setup();
    renderAccess();
    await user.click(await screen.findByLabelText("Anyone with the link"));
    const input = document.getElementById("share-anyone-expires") as HTMLInputElement;
    expect(input.getAttribute("type")).toBe("date");
    expect(input.getAttribute("max")).toBe(dateInputValue(latestAnyoneEnd(new Date(), 10)));
  });

  it("a refused link shows the text for its code, never the server text", async () => {
    const user = userEvent.setup();
    const refused = new ApiError(403, "forbidden", { reason: "public_links_off" }, 0);
    Object.defineProperty(refused, "message", { value: "SERVER TEXT" });
    api.createShareLink.mockRejectedValueOnce(refused);
    renderAccess();
    await user.click(await screen.findByLabelText("Anyone with the link"));
    await user.click(screen.getByRole("button", { name: "Create link" }));
    expect(await screen.findByText(/Links that anyone can open are turned off/)).toBeTruthy();
    expect(screen.queryByText("SERVER TEXT")).toBeNull();
  });

  it("an unknown refusal shows the generic text, never the server text", async () => {
    const user = userEvent.setup();
    const refused = new ApiError(500, "internal", {}, 0);
    Object.defineProperty(refused, "message", { value: "SERVER TEXT" });
    api.createShareLink.mockRejectedValueOnce(refused);
    renderAccess();
    await user.click(await screen.findByLabelText("Anyone with the link"));
    await user.click(screen.getByRole("button", { name: "Create link" }));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("SERVER TEXT")).toBeNull();
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("a member who may not share sees no link choices", async () => {
    renderAccess("viewer", null);
    await screen.findByText("Restricted: only people with a role on this item");
    expect(screen.queryByLabelText("Anyone with the link")).toBeNull();
    const organisation = screen.getByLabelText("Anyone in Acme") as HTMLInputElement;
    expect(organisation.disabled).toBe(true);
  });

  it("a session change while a change is out drops its answer", async () => {
    const user = userEvent.setup();
    const { onchanged } = renderAccess();
    await user.click(await screen.findByLabelText("Anyone in Acme"));
    let answer!: (v: unknown) => void;
    api.createShareLink.mockReturnValueOnce(new Promise((r) => (answer = r)));
    await user.click(screen.getByRole("button", { name: "Create link" }));
    session.set(null);
    answer({ link: { id: "shl_2" }, url: SECRET_URL });
    await new Promise((r) => setTimeout(r, 50));
    expect(onchanged).not.toHaveBeenCalled();
  });
});

const ORG_LINK = {
  id: "shl_o",
  node_id: "nod_board",
  audience: "organisation",
  role: "viewer",
  expires_at: "2026-12-01T00:00:00Z",
  created_at: "2026-10-01T00:00:00Z",
  inherited_from: null,
};

describe("the links that cannot be read", () => {
  beforeEach(() => {
    session.set(person(true));
    maxExpiryDays.set(0);
    passwordMinLength.set(8);
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("a failed first load shows the failure and no choices (guard: the isError branch, !data)", async () => {
    api.listShareLinks.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    renderAccess();
    await screen.findByRole("alert");
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
  });

  it("a failed refetch of a list already shown keeps the list (guard: the !data test)", async () => {
    api.listShareLinks.mockResolvedValueOnce({ items: [ORG_LINK] });
    const { client } = renderAccess();
    await screen.findByLabelText("Anyone in Acme");
    api.listShareLinks.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() => expect(api.listShareLinks.mock.calls.length).toBeGreaterThan(1));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryAllByRole("radio").length).toBeGreaterThan(0);
  });

  it("an input event on a date sends no change, and a change event sends one (guard: the native change listener)", async () => {
    api.listShareLinks.mockResolvedValue({ items: [ORG_LINK] });
    api.updateShareLink.mockResolvedValue({});
    renderAccess();
    const input = (await waitFor(() => {
      const el = document.getElementById("share-general-expires") as HTMLInputElement | null;
      if (!el) throw new Error("no date input yet");
      return el;
    })) as HTMLInputElement;
    fireEvent.input(input, { target: { value: "2027-01-05" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(api.updateShareLink).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "2027-01-05" } });
    await waitFor(() => expect(api.updateShareLink).toHaveBeenCalledTimes(1));
  });

  it("an input event on the end date of an anyone link sends no change, and a change event sends one (guard: the native change listener)", async () => {
    const ANYONE_LINK = { id: "shl_2", node_id: "nod_board", audience: "anyone", role: "viewer", expires_at: "2027-02-01T00:00:00Z", created_at: "", inherited_from: null, password_set: false, paused: false };
    api.listShareLinks.mockResolvedValue({ items: [ANYONE_LINK] });
    api.updateShareLink.mockResolvedValue({});
    renderAccess();
    const input = (await waitFor(() => {
      const el = document.getElementById("share-general-expires") as HTMLInputElement | null;
      if (!el) throw new Error("no date input yet");
      return el;
    })) as HTMLInputElement;
    fireEvent.input(input, { target: { value: "2027-01-05" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(api.updateShareLink).not.toHaveBeenCalled();
    fireEvent.change(input, { target: { value: "2027-01-05" } });
    await waitFor(() => expect(api.updateShareLink).toHaveBeenCalledTimes(1));
  });

  it("the end of an organisation link stops at the sharing cap, as the picker offers it (guard: expiryFromInputCapped)", async () => {
    maxExpiryDays.set(10);
    api.listShareLinks.mockResolvedValue({ items: [ORG_LINK] });
    api.updateShareLink.mockResolvedValue({});
    renderAccess();
    const input = (await waitFor(() => {
      const el = document.getElementById("share-general-expires") as HTMLInputElement | null;
      if (!el) throw new Error("no date input yet");
      return el;
    })) as HTMLInputElement;
    const lastDay = dateInputValue(latestAnyoneEnd(new Date(), 10));
    fireEvent.change(input, { target: { value: lastDay } });
    await waitFor(() => expect(api.updateShareLink).toHaveBeenCalledTimes(1));
    const sent = new Date((api.updateShareLink.mock.calls[0][1] as { expires_at: string }).expires_at).getTime();
    expect(sent).toBeLessThanOrEqual(Date.now() + 10 * 24 * 60 * 60 * 1000 + 1000);
  });
});

