// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import type { Node } from "../../lib/files/types";

const apiMock = vi.hoisted(() => vi.fn());
// The other calls of the drawer (labels, comments) keep the real module; only the calls that end a session are replaced.
vi.mock("../../lib/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../lib/api")>()), api: apiMock }));
// ended answers the calls of the drawer's other parts with empty pages, and the session's end with the given answer.
function ended(answer: () => Promise<unknown>) {
  apiMock.mockImplementation((_method: string, path: string) =>
    path.endsWith("/unlock") ? answer() : Promise.resolve({ items: [], next_cursor: "" }),
  );
}

import Details from "./Details";

function render(ui: ReactElement) {
  return rtlRender(<QueryClientProvider client={createQueryClient()}>{ui}</QueryClientProvider>);
}

function file(over: Partial<Node> = {}): Node {
  return {
    id: "nod_a1",
    folder_id: "fld_b",
    parent_id: null,
    name: "letter.docx",
    kind: "file",
    size: 5,
    mime_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-02T00:00:00Z",
    role: "manager",
    no_download: false,
    editors_can_share: false,
    editing: true,
    editing_by: "Cy Rivera",
    ...over,
  };
}

// modalOf waits for the confirmation (an antd modal) and returns its box.
async function modalOf(): Promise<HTMLElement> {
  await waitFor(() => expect(document.querySelector(".ant-modal")).not.toBeNull());
  return document.querySelector(".ant-modal") as HTMLElement;
}

function open(node: Node, onChanged = vi.fn()) {
  return render(
    <Details node={node} canVersions={false} canShare={false} onClose={() => undefined} onVersions={() => undefined}
      onShare={() => undefined} onChanged={onChanged} />,
  );
}

describe("the lock in Details", () => {
  beforeEach(() => {
    apiMock.mockReset();
    ended(() => Promise.resolve(undefined));
  });

  it("says who is editing the file", () => {
    open(file());
    expect(screen.getByText("Being edited")).toBeTruthy();
    expect(screen.getByText("Cy Rivera")).toBeTruthy();
  });

  it("offers the end of the editing session to a manager, behind a confirmation that warns of lost edits", async () => {
    const onChanged = vi.fn();
    open(file(), onChanged);
    fireEvent.click(screen.getByRole("button", { name: "End the editing session" }));
    const dialog = await modalOf();
    expect(within(dialog).getByText(/unsaved changes/i)).toBeTruthy();
    expect(apiMock.mock.calls.some((c) => String(c[1]).endsWith("/unlock"))).toBe(false);
    fireEvent.click(within(dialog).getByRole("button", { name: "End the session" }));
    await waitFor(() => expect(apiMock).toHaveBeenCalledWith("POST", "/api/v1/nodes/nod_a1/unlock"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledWith(expect.objectContaining({ editing: false })));
  });

  it("does not offer it to an editor, and does not offer it for a file no one is editing", () => {
    const { unmount } = open(file({ role: "editor" }));
    expect(screen.queryByRole("button", { name: "End the editing session" })).toBeNull();
    unmount();
    open(file({ editing: false, editing_by: undefined }));
    expect(screen.queryByText("Being edited")).toBeNull();
    expect(screen.queryByRole("button", { name: "End the editing session" })).toBeNull();
  });

  it("shows a conflict by name when the file is no longer held", async () => {
    ended(() => Promise.reject(new ApiError(409, "conflict", {}, 0)));
    open(file());
    fireEvent.click(screen.getByRole("button", { name: "End the editing session" }));
    const dialog = await modalOf();
    fireEvent.click(within(dialog).getByRole("button", { name: "End the session" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("No editor holds this file any more.");
  });
});
