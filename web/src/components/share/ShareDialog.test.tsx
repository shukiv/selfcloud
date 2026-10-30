// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { session, type Session } from "../../lib/session";
import { maxExpiryDays } from "../../lib/sharing/limits";
import { passwordMinLength } from "../../lib/brand";
import type { FolderAccess, Node, Permission, Role } from "../../lib/files/types";

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
}));

vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));
vi.mock("../../lib/manage/api", () => ({ deleteAllPublicLinks: api.deleteAllPublicLinks }));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), statOrNull: api.statOrNull }));

import ShareDialog from "./ShareDialog";

function person(id: string, publicLinks = false): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en" },
    account: { id: "acc_1", name: "Acme" },
    csrf: `csrf-${id}`,
    publicLinks,
  } as unknown as Session;
}

function nodeOf(role: Role | undefined, extra: Partial<Node> = {}): Node {
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
    ...extra,
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

const own: Permission = {
  subject_kind: "user",
  subject_id: "usr_ann",
  role: "manager",
  inherited_from: null,
  expires_at: null,
  created_at: "2026-10-01T00:00:00Z",
  created_by: "usr_ann",
  subject_name: "Ann Manager",
};

const mate: Permission = {
  subject_kind: "user",
  subject_id: "usr_mate",
  role: "viewer",
  inherited_from: null,
  expires_at: null,
  created_at: "2026-10-01T00:00:00Z",
  created_by: "usr_ann",
  subject_name: "Mateo Member",
};

// pick opens a select by its id and clicks the option with the label, as a person does.
async function pick(user: ReturnType<typeof userEvent.setup>, id: string, label: string) {
  await user.click(document.getElementById(id) as HTMLElement);
  await user.click(await screen.findByText(label, { selector: ".ant-select-item-option-content" }));
}

// optionLabels lists the options a select offers, in order, from the dropdown that is open.
async function optionLabels(user: ReturnType<typeof userEvent.setup>, id: string): Promise<string[]> {
  await user.click(document.getElementById(id) as HTMLElement);
  await screen.findByText("Can view", { selector: ".ant-select-item-option-content" });
  return Array.from(document.querySelectorAll(".ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option-content")).map(
    (el) => el.textContent ?? "",
  );
}

function renderDialog(node: Node, props: { onclose?: () => void; onchanged?: () => void; space?: FolderAccess | null } = {}) {
  const client = createQueryClient();
  const onclose = props.onclose ?? vi.fn();
  const onchanged = props.onchanged ?? vi.fn();
  const ui = (
    <QueryClientProvider client={client}>
      <ShareDialog node={node} space={props.space === undefined ? TEAM : props.space} onclose={onclose} onchanged={onchanged} />
    </QueryClientProvider>
  );
  const view = render(ui);
  return { ...view, client, onclose, onchanged, ui };
}

describe("the share dialog", () => {
  beforeEach(() => {
    session.set(person("usr_ann", true));
    maxExpiryDays.set(0);
    passwordMinLength.set(8);
    api.listPermissions.mockResolvedValue({ items: [own, mate] });
    api.listShareLinks.mockResolvedValue({ items: [] });
    api.statOrNull.mockResolvedValue(null);
    api.setPermission.mockResolvedValue({ ...mate, notified: true });
    api.removePermission.mockResolvedValue(undefined);
    api.updateNodeFlags.mockResolvedValue({});
    api.searchDirectory.mockResolvedValue({ items: [] });
  });
  afterEach(() => {
    vi.clearAllMocks();
    session.set(null);
  });

  it("a manager sees the people list, the add form and the advanced section", async () => {
    renderDialog(nodeOf("manager"));
    expect(await screen.findByText("Mateo Member")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "People with access" })).toBeTruthy();
    expect(document.getElementById("share-who")).not.toBeNull();
    expect(screen.getByRole("heading", { name: "Advanced" })).toBeTruthy();
  });

  it("a viewer sees only its own row and no share controls", async () => {
    api.listPermissions.mockResolvedValue({ items: [mate] });
    renderDialog(nodeOf("viewer"));
    expect(await screen.findByRole("heading", { name: "Your access" })).toBeTruthy();
    expect(screen.getByText("You have Can view access to this item.")).toBeTruthy();
    expect(document.getElementById("share-who")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Advanced" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    expect(screen.getByText("You can see who has access, but only a manager of this item can share it.")).toBeTruthy();
  });

  it("a contributor is told it cannot share, and sees no role choices", async () => {
    api.listPermissions.mockResolvedValue({ items: [mate] });
    renderDialog(nodeOf("contributor"));
    expect(await screen.findByText("You can see who has access, but only a manager of this item can share it.")).toBeTruthy();
    expect(document.getElementById("share-role")).toBeNull();
  });

  it("an editor may share only when the item allows editors to share, and then offers viewer to editor", async () => {
    api.listPermissions.mockResolvedValue({ items: [mate] });
    const user = userEvent.setup();
    const { unmount } = renderDialog(nodeOf("editor", { editors_can_share: true }));
    await screen.findByRole("heading", { name: "Add people" });
    expect(await optionLabels(user, "share-role")).toEqual(["Can view", "Can comment", "Can upload", "Can edit"]);
    unmount();
  });

  it("a manager is offered every role in the role choice of the add form", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByRole("heading", { name: "Add people" });
    expect(await optionLabels(user, "share-role")).toEqual(["Can view", "Can comment", "Can upload", "Can edit", "Manager"]);
  });

  it("a role change is saved without mail", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByText("Mateo Member");
    await pick(user, "role-usr_mate", "Can edit");
    await waitFor(() => expect(api.setPermission).toHaveBeenCalledWith("nod_board", "usr_mate", { role: "editor", notify: false }));
  });

  it("removing a person asks first", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByText("Mateo Member");
    await user.click(screen.getAllByRole("button", { name: "Remove" })[0]);


    expect(api.removePermission).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Remove access" }));
    await waitFor(() => expect(api.removePermission).toHaveBeenCalledWith("nod_board", "usr_mate"));
  });

  it("no-download is saved as a flag on the node", async () => {
    const user = userEvent.setup();
    renderDialog(nodeOf("manager"));
    await screen.findByText("Mateo Member");
    await user.click(screen.getByLabelText("Viewers and commenters can't download"));
    await waitFor(() => expect(api.updateNodeFlags).toHaveBeenCalledWith("nod_board", { no_download: true }));
    expect(await screen.findByText("Viewers and commenters can't download")).toBeTruthy();
  });

  it("names an ancestor with no name by its place, never as a blank", async () => {
    api.listPermissions.mockResolvedValue({ items: [own, { ...mate, inherited_from: "nod_rootaaaa" }] });
    api.statOrNull.mockResolvedValue({ id: "nod_rootaaaa", name: "" });
    renderDialog(nodeOf("manager"));
    // The name is read after the rows, so the place shows first and the blank name would replace it: wait for both.
    await waitFor(() => expect(api.statOrNull).toHaveBeenCalledWith("nod_rootaaaa"));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(screen.getByText("from a parent folder")).toBeTruthy();
  });

  it("a failed refetch keeps the list on screen", async () => {
    const { client } = renderDialog(nodeOf("manager"));
    await screen.findByText("Mateo Member");
    // A refusal that is not retried, so the failed refetch is the state the page holds at the check below.
    api.listPermissions.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() => expect(api.listPermissions.mock.calls.length).toBeGreaterThan(1));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Mateo Member")).toBeTruthy();
  });

  it("Escape closes the dialog, and a click on the mask does not", async () => {
    const onclose = vi.fn();
    renderDialog(nodeOf("manager"), { onclose });
    await screen.findByText("Mateo Member");
    const wrap = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.mouseDown(wrap);
    fireEvent.click(wrap);
    expect(onclose).not.toHaveBeenCalled();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", code: "Escape", keyCode: 27 });
    await waitFor(() => expect(onclose).toHaveBeenCalled());
  });

  it("a session change closes the dialog, so the next person never sees its state", async () => {
    const onclose = vi.fn();
    renderDialog(nodeOf("manager"), { onclose });
    await screen.findByText("Mateo Member");
    act(() => session.set(person("usr_bob")));
    await waitFor(() => expect(onclose).toHaveBeenCalled());
  });

  it("a refused change shows the catalogue text for its code, never the server text", async () => {
    const user = userEvent.setup();
    api.setPermission.mockRejectedValueOnce(new ApiError(403, "forbidden", {}, 0));
    renderDialog(nodeOf("manager"));
    await screen.findByText("Mateo Member");
    await pick(user, "role-usr_mate", "Can edit");
    expect(await screen.findByText("You can't change who has access to this item.")).toBeTruthy();
  });
});
