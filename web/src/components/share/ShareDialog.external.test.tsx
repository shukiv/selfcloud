// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { session, type Session } from "../../lib/session";
import { maxExpiryDays } from "../../lib/sharing/limits";
import { passwordMinLength } from "../../lib/brand";
import type { FolderAccess, Node, Role } from "../../lib/files/types";

// The sharing API and the stat read are replaced, so each test states what the server answers.
const api = vi.hoisted(() => ({
  listPermissions: vi.fn(),
  setPermission: vi.fn(),
  removePermission: vi.fn(),
  updateNodeFlags: vi.fn(),
  transferOwnership: vi.fn(),
  listShareLinks: vi.fn(),
  createShareLink: vi.fn(),
  updateShareLink: vi.fn(),
  deleteShareLink: vi.fn(),
  resetShareLink: vi.fn(),
  createDropLink: vi.fn(),
  updateDropLink: vi.fn(),
  searchDirectory: vi.fn(),
  createAccessRequest: vi.fn(),
  updateSpaceSettings: vi.fn(),
  getSharingPolicy: vi.fn(),
  setSharingPolicy: vi.fn(),
  deleteAllPublicLinks: vi.fn(),
  statOrNull: vi.fn(),
  listExternalPermissions: vi.fn(),
  addExternalPermission: vi.fn(),
  removeExternalPermission: vi.fn(),
}));

vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));
vi.mock("../../lib/manage/api", () => ({ deleteAllPublicLinks: api.deleteAllPublicLinks }));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), statOrNull: api.statOrNull }));

import ShareDialog from "./ShareDialog";

const OFF_HINT = "To share with people in other accounts, your account's owner can allow it in Manage > Overview.";

function person(id: string, externalSharing: boolean): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en" },
    account: { id: "acc_1", name: "Acme" },
    csrf: `csrf-${id}`,
    publicLinks: false,
    accountExternalSharing: externalSharing,
  } as unknown as Session;
}

function nodeOf(role: Role | undefined): Node {
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
    owner_id: "usr_owner",
  };
}

const TEAM: FolderAccess = {
  id: "fld_team",
  name: "Team",
  kind: "team",
  access: "write",
  role: "editor",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: false,
};

const grant = {
  email: "mia@else.example",
  role: "viewer",
  created_at: "2026-11-01T00:00:00Z",
  expires_at: null,
  created_by_name: "Ann Manager",
};

function renderDialog(node: Node) {
  const client = createQueryClient();
  const onchanged = vi.fn();
  const view = render(
    <QueryClientProvider client={client}>
      <ShareDialog node={node} space={TEAM} onclose={vi.fn()} onchanged={onchanged} />
    </QueryClientProvider>,
  );
  return { ...view, client, onchanged };
}

// typeAddress types a full address into the add form's combobox and waits for the search to answer.
async function typeAddress(user: ReturnType<typeof userEvent.setup>, address: string) {
  await user.type(document.getElementById("share-who") as HTMLElement, address);
  await waitFor(() => expect(api.searchDirectory).toHaveBeenCalledWith(address, "all"));
}

describe("sharing with an address outside the account", () => {
  beforeEach(() => {
    session.set(person("usr_ann", true));
    maxExpiryDays.set(0);
    passwordMinLength.set(8);
    api.listPermissions.mockResolvedValue({ items: [] });
    api.listShareLinks.mockResolvedValue({ items: [] });
    api.statOrNull.mockResolvedValue(null);
    api.searchDirectory.mockResolvedValue({ items: [] });
    api.listExternalPermissions.mockResolvedValue({ items: [] });
    api.addExternalPermission.mockResolvedValue(grant);
    api.removeExternalPermission.mockResolvedValue(undefined);
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("the outside option adds the address as a viewer, with no role choice and no mail", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    // The role is fixed to viewer: no select, and no message or notify for an outside share.
    expect(document.getElementById("share-role")).toBeNull();
    expect(document.getElementById("share-message")).toBeNull();
    expect(document.getElementById("share-notify")).toBeNull();
    expect(screen.getAllByText("Can view").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() => expect(api.addExternalPermission).toHaveBeenCalledWith("nod_board", { email: "mia@else.example", role: "viewer" }));
    expect(api.setPermission).not.toHaveBeenCalled();
    expect(await screen.findByText("Shared with mia@else.example as Can view.")).toBeTruthy();
  });

  it("the end date the person row uses is sent with the outside share", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    fireEvent.change(document.getElementById("share-expires") as HTMLElement, { target: { value: "2027-06-01" } });
    await user.click(screen.getByRole("button", { name: "Share" }));
    await waitFor(() => expect(api.addExternalPermission).toHaveBeenCalled());
    expect(api.addExternalPermission).toHaveBeenCalledWith("nod_board", {
      email: "mia@else.example",
      role: "viewer",
      expires_at: new Date(2027, 5, 1, 23, 59, 59, 0).toISOString(),
    });
  });

  it("with the account's switch off there is no option, and the hint says where it is turned on", async () => {
    session.set(person("usr_ann", false));
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await waitFor(() => expect(screen.queryByText("Share with mia@else.example")).toBeNull());
    expect(screen.getByText(OFF_HINT)).toBeTruthy();
  });

  it("a same-account refusal names the address and says to share with the person here", async () => {
    api.addExternalPermission.mockRejectedValue(new ApiError(400, "invalid_request", { reason: "same_account" }, 0));
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    await user.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByText("mia@else.example is in your account; share with them as a person here.")).toBeTruthy();
  });

  it("a switch turned off meanwhile says the same hint as the form", async () => {
    api.addExternalPermission.mockRejectedValue(new ApiError(403, "forbidden", { reason: "external_sharing_off" }, 0));
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    await user.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByText(OFF_HINT)).toBeTruthy();
  });

  it("too many outside shares says so", async () => {
    api.addExternalPermission.mockRejectedValue(new ApiError(409, "conflict", { reason: "too_many_external" }, 0));
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    await typeAddress(user, "mia@else.example");
    await user.click(await screen.findByText("Share with mia@else.example"));
    await user.click(screen.getByRole("button", { name: "Share" }));
    expect(await screen.findByText("This item has the most shares with other accounts it can have. Remove one before adding another.")).toBeTruthy();
  });
});

describe("the outside grants of an item", () => {
  beforeEach(() => {
    session.set(person("usr_ann", true));
    maxExpiryDays.set(0);
    passwordMinLength.set(8);
    api.listPermissions.mockResolvedValue({ items: [] });
    api.listShareLinks.mockResolvedValue({ items: [] });
    api.statOrNull.mockResolvedValue(null);
    api.searchDirectory.mockResolvedValue({ items: [] });
    api.listExternalPermissions.mockResolvedValue({ items: [grant] });
    api.removeExternalPermission.mockResolvedValue(undefined);
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("lists the address apart from the people, tagged, as a viewer, with its end date", async () => {
    api.listExternalPermissions.mockResolvedValue({ items: [{ ...grant, expires_at: "2026-12-01T00:00:00Z" }] });
    renderDialog(nodeOf("manager"));
    expect(await screen.findByText("mia@else.example")).toBeTruthy();
    expect(screen.getByText("Other account")).toBeTruthy();
    expect(screen.getByText("Can view")).toBeTruthy();
    expect(screen.getByText(/^Ends /)).toBeTruthy();
    expect(api.listExternalPermissions).toHaveBeenCalledWith("nod_board");
  });

  it("removing one deletes the encoded address and reads the list again", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByText("mia@else.example");
    const before = api.listExternalPermissions.mock.calls.length;
    await user.click(screen.getByRole("button", { name: "Remove: mia@else.example" }));
    await waitFor(() => expect(api.removeExternalPermission).toHaveBeenCalledWith("nod_board", "mia@else.example"));
    await waitFor(() => expect(api.listExternalPermissions.mock.calls.length).toBeGreaterThan(before));
    expect(await screen.findByText("mia@else.example no longer has access.")).toBeTruthy();
  });

  it("a caller who may not share sees the grant but no remove icon", async () => {
    renderDialog(nodeOf("viewer"));
    expect(await screen.findByText("mia@else.example")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Remove: mia@else.example" })).toBeNull();
  });

  it("a failed read of the grants keeps the people list and shows no error of its own", async () => {
    api.listExternalPermissions.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    renderDialog(nodeOf("manager"));
    expect(await screen.findByRole("heading", { name: "Add people" })).toBeTruthy();
    await waitFor(() => expect(api.listExternalPermissions).toHaveBeenCalled());
    expect(screen.queryByText("mia@else.example")).toBeNull();
  });
});