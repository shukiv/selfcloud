// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App as AntApp } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";

const m = vi.hoisted(() => ({
  getTemplatesFolder: vi.fn(),
  setTemplatesFolder: vi.fn(),
  statOrNull: vi.fn(),
  myFolders: vi.fn(),
  pick: { folder: "fld_abcdefgh", dir: "nod_abcdefgh" as string | null },
}));
vi.mock("../../lib/files/api", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/files/api")>();
  return { ...real, statOrNull: m.statOrNull, myFolders: m.myFolders };
});
vi.mock("../../lib/files/templates", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/files/templates")>();
  return { ...real, getTemplatesFolder: m.getTemplatesFolder, setTemplatesFolder: m.setTemplatesFolder };
});

// The folder picker is a stub: it offers one directory and reports the choice.
vi.mock("../files/PickDestination", () => ({
  default: ({ onPick }: { onPick: (d: { folder: string; dir: string | null }) => void }) => (
    <button type="button" data-testid="pick-stub" onClick={() => onPick(m.pick)}>
      pick
    </button>
  ),
}));

import TemplatesFolderSetting from "./TemplatesFolderSetting";

const ACC = "acc_abcdefgh";

function signed(role: "member" | "admin", owner: boolean): Session {
  return {
    user: { id: "u1", email: "u1@example.com", display_name: "u1", role: owner ? "owner" : role, locale: "en" } as Session["user"],
    account: { id: ACC, owner: owner } as unknown as Session["account"],
    csrf: "t",
    publicLinks: false,
  };
}

function renderFor(s: Session | null) {
  session.set(s);
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AntApp>
        <TemplatesFolderSetting account={ACC} />
      </AntApp>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  m.getTemplatesFolder.mockReset();
  m.setTemplatesFolder.mockReset();
  m.statOrNull.mockReset();
  m.myFolders.mockReset();
  m.pick = { folder: "fld_abcdefgh", dir: "nod_abcdefgh" };
  m.getTemplatesFolder.mockResolvedValue("nod_abcdefgh");
  m.statOrNull.mockResolvedValue({ id: "nod_abcdefgh", name: "Templates" });
  m.myFolders.mockResolvedValue([]);
});
afterEach(() => session.set(null));

describe("TemplatesFolderSetting", () => {
  it("members see nothing", async () => {
    const view = renderFor(signed("member", false));
    await new Promise((r) => setTimeout(r, 10));
    expect(view.container.textContent).toBe("");
    expect(m.getTemplatesFolder).not.toHaveBeenCalled();
  });

  it("the account's owner sees the control, with the folder it has", async () => {
    renderFor(signed("member", true));
    expect(await screen.findByText("Templates")).toBeTruthy();
    expect(m.getTemplatesFolder).toHaveBeenCalledWith(ACC);
  });

  it("saves the folder picked, with PUT", async () => {
    m.setTemplatesFolder.mockResolvedValue(null);
    renderFor(signed("member", true));
    await screen.findByText("Templates");
    fireEvent.click(screen.getByRole("button", { name: /change|choose|pick/i }));
    fireEvent.click(await screen.findByTestId("pick-stub"));
    await waitFor(() => expect(m.setTemplatesFolder).toHaveBeenCalledWith(ACC, "nod_abcdefgh"));
  });

  // A space's root is picked with dir null: the folder list gives the root node, and that is what is saved.
  it("saves the root node of a space picked at its root", async () => {
    m.setTemplatesFolder.mockResolvedValue(null);
    m.myFolders.mockResolvedValue([{ id: "fld_abcdefgh", name: "Team", kind: "team", access: "write", role: "manager", root_node_id: "nod_rootroot" }]);
    m.pick = { folder: "fld_abcdefgh", dir: null };
    renderFor(signed("member", true));
    await screen.findByText("Templates");
    fireEvent.click(screen.getByRole("button", { name: /change|choose|pick/i }));
    fireEvent.click(await screen.findByTestId("pick-stub"));
    await waitFor(() => expect(m.setTemplatesFolder).toHaveBeenCalledWith(ACC, "nod_rootroot"));
  });

  it("clears the setting with null", async () => {
    m.setTemplatesFolder.mockResolvedValue(null);
    renderFor(signed("member", true));
    await screen.findByText("Templates");
    fireEvent.click(screen.getByRole("button", { name: /clear|remove/i }));
    await waitFor(() => expect(m.setTemplatesFolder).toHaveBeenCalledWith(ACC, null));
  });

  it("an error keeps the old value and says so", async () => {
    m.setTemplatesFolder.mockRejectedValue(new Error("refused"));
    renderFor(signed("member", true));
    await screen.findByText("Templates");
    fireEvent.click(screen.getByRole("button", { name: /clear|remove/i }));
    await screen.findByRole("alert");
    expect(screen.getByText("Templates")).toBeTruthy();
  });
});
