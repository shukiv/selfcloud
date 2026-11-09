// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { App as AntdApp } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setLang } from "../../lib/i18n";
import NewFolderDialog from "./NewFolderDialog";
import { refreshTree } from "../../lib/query";

const mocks = vi.hoisted(() => ({ mkdir: vi.fn(), mkdirIn: vi.fn() }));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), mkdir: mocks.mkdir, mkdirIn: mocks.mkdirIn }));
vi.mock("../../lib/query", () => ({ refreshTree: vi.fn() }));

const where = { folder: "fld_mine", dirID: null, path: "/" };

function renderDialog(onClose = vi.fn(), place: { folder: string; dirID: string | null; path: string | null } = where) {
  const qc = new QueryClient();
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NewFolderDialog where={place} openable={false} onClose={onClose} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { onClose };
}

describe("the new folder dialog", () => {
  beforeEach(async () => {
    await setLang("en");
    mocks.mkdir.mockReset();
  });

  it("finds the name input by its label", () => {
    renderDialog();
    const input = screen.getByLabelText("Folder name");
    expect(input.tagName).toBe("INPUT");
    expect(input.id).toBe("newfolder-name");
  });

  // nt2 round 3: the name input has focus as soon as the dialog mounts. A person who types right after Alt+C then F must not lose the
  // first letters to the New button, so the focus does not wait for antd's open animation (afterOpenChange).
  it("has the focus on the name input as soon as the dialog renders, not after its open animation", async () => {
    renderDialog();
    const input = screen.getByLabelText("Folder name");
    await waitFor(() => expect(document.activeElement).toBe(input));
  });

  // Slice fu3b (decision 77, item 4): the files page's own dialog is this one. A folder made re-reads the branch it is in, which is
  // where the page's listing comes from, so the page shows it without a reload of its own.
  it("re-reads the branch the folder was made in, and closes (fu3b)", async () => {
    mocks.mkdir.mockResolvedValue({ id: "nod_new", name: "Plans", kind: "dir" });
    const onClose = vi.fn();
    renderDialog(onClose);
    fireEvent.change(screen.getByLabelText("Folder name"), { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(refreshTree).toHaveBeenCalledWith(expect.anything(), [{ folder: "fld_mine", dir: null }]);
  });

  // The Shell closes the sidebar's dialog on a change of address by unmounting it. A folder whose call is still in flight then
  // is still made, and its result is still told to the person (fu3b review 6).
  it("tells the person the folder was made when the dialog was unmounted while the call was in flight (fu3b review 6)", async () => {
    let finish!: (node: { id: string; name: string; kind: string }) => void;
    mocks.mkdir.mockImplementation(() => new Promise((resolve) => (finish = resolve)));
    const qc = new QueryClient();
    // The app's antd App stays mounted above the Shell; only the dialog is unmounted here, as a route change does it.
    const tree = (open: boolean) => (
      <QueryClientProvider client={qc}>
        <AntdApp>
          <MemoryRouter>{open && <NewFolderDialog where={where} openable onClose={vi.fn()} />}</MemoryRouter>
        </AntdApp>
      </QueryClientProvider>
    );
    const { rerender } = render(tree(true));
    fireEvent.change(screen.getByLabelText("Folder name"), { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(mocks.mkdir).toHaveBeenCalled());
    rerender(tree(false));
    await act(async () => {
      finish({ id: "nod_new", name: "Plans", kind: "dir" });
    });
    await waitFor(() => expect(document.body.textContent).toContain("Plans"));
  });

  it("names the directory by its ID when the folder is made in a directory (decision 74)", async () => {
    mocks.mkdirIn.mockReset();
    mocks.mkdirIn.mockReturnValue(new Promise(() => undefined));
    renderDialog(vi.fn(), { folder: "fld_mine", dirID: "nod_docs", path: "/Docs" });
    fireEvent.change(screen.getByLabelText("Folder name"), { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(mocks.mkdirIn).toHaveBeenCalledWith("fld_mine", "nod_docs", "Plans"));
    expect(mocks.mkdir).not.toHaveBeenCalled();
  });

  it("shows the create button as loading while the folder is being made", async () => {
    mocks.mkdir.mockReturnValue(new Promise(() => undefined));
    renderDialog();
    fireEvent.change(screen.getByLabelText("Folder name"), { target: { value: "Plans" } });
    const create = screen.getByRole("button", { name: "Create folder" });
    expect(create.className).not.toContain("ant-btn-loading");
    fireEvent.click(create);
    await waitFor(() => expect(mocks.mkdir).toHaveBeenCalledTimes(1));
    // The loading icon adds its own name to the button, so the button is found by its class.
    await waitFor(() => expect(document.querySelector(".ant-modal-footer .ant-btn-primary")?.className).toContain("ant-btn-loading"));
  });
});
