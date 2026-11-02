// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setLang } from "../../lib/i18n";
import NewFolderDialog from "./NewFolderDialog";

const mocks = vi.hoisted(() => ({ mkdir: vi.fn() }));
vi.mock("../../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../../lib/files/api")>()), mkdir: mocks.mkdir }));
vi.mock("../../lib/query", () => ({ refreshTree: vi.fn() }));

const where = { folder: "fld_mine", dirID: null, path: "/" };

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient();
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NewFolderDialog where={where} openable={false} onClose={onClose} />
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
