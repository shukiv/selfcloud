// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";

// The page's calls to the API and its navigation are replaced: the test checks what the page sends and where it goes.
const apiMock = vi.hoisted(() => vi.fn());
const goMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", () => ({ api: apiMock }));
vi.mock("../lib/files/api", () => ({
  statNode: () =>
    Promise.resolve({ id: "nod_a1", folder_id: "fld_b", name: "letter.docx", kind: "file", role: "editor" }),
}));
vi.mock("../lib/router", () => ({ go: goMock }));

import OfficeEditPage from "./OfficeEditPage";
import { officeEdit } from "../lib/files/editor";

const TOKEN = "sct_abc123.mac456";
const ACTION = "https://office.example.test/hosting/x?WOPISrc=https%3A%2F%2Fselfcloud.example.test%2Fwopi%2Ffiles%2Fnod_a1";
const NODE = {
  id: "nod_a1",
  folder_id: "fld_b",
  parent_id: null,
  name: "letter.docx",
  kind: "file",
  size: 5,
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "editor",
  no_download: false,
  editors_can_share: false,
};

function render(path: string) {
  return rtlRender(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        {/* The app mounts every page under one catch-all route (lib/router.ts), so the page reads its node from the address. */}
        <Route path="*" element={<OfficeEditPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

// answerAll gives the page its file and the editor's action, as the API does.
function answerAll(office: () => Promise<unknown>) {
  apiMock.mockImplementation((method: string, path: string) => {
    if (method === "GET" && path === "/api/v1/nodes/nod_a1") return Promise.resolve(NODE);
    if (method === "POST" && path === "/api/v1/nodes/nod_a1/office") return office();
    return Promise.reject(new Error(`unexpected ${method} ${path}`));
  });
}

describe("the editor page", () => {
  let submit: ReturnType<typeof vi.spyOn>;
  let logged: unknown[][];
  // posted is each form the page submitted, kept as it was at the submit.
  let posted: HTMLFormElement[];

  beforeEach(() => {
    apiMock.mockReset();
    // The server offers the editor (office_edit) in these tests; a test with the feature off is the page's own gate.
    officeEdit.set(true);
    goMock.mockReset();
    logged = [];
    posted = [];
    submit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(function (this: HTMLFormElement) {
      posted.push(this);
    });
    for (const level of ["log", "info", "warn", "error", "debug"] as const) {
      vi.spyOn(console, level).mockImplementation((...args: unknown[]) => {
        logged.push(args);
      });
    }
    window.history.replaceState(null, "", "/");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("posts the token into the editor's form, in the body, and never in the address or the console", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");

    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    const form = posted[0];
    expect(form?.target).toBe("selfcloud-office-editor");
    expect(form?.method.toLowerCase()).toBe("post");
    expect(form?.action).toBe(ACTION);
    expect(form?.querySelector<HTMLInputElement>('input[name="access_token"]')?.value).toBe(TOKEN);
    // Once posted, the form leaves the page, so no script of the page can read the token from it (review of decision 73).
    expect(document.querySelector('input[name="access_token"]')).toBeNull();
    expect(document.querySelector('form[target="selfcloud-office-editor"]')).toBeNull();
    expect(window.location.href).not.toContain(TOKEN);
    expect(document.querySelector("iframe[name='selfcloud-office-editor']")).not.toBeNull();
    expect(JSON.stringify(logged)).not.toContain(TOKEN);
    // The open is a POST with the mode in its body, and no token in what the page sent.
    expect(apiMock).toHaveBeenCalledWith("POST", "/api/v1/nodes/nod_a1/office", { body: { mode: "edit" } });
  });

  it("ignores a message from another origin, and a message from another window", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    const frame = document.querySelector<HTMLIFrameElement>("iframe[name='selfcloud-office-editor']");

    await act(async () => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ MessageId: "UI_Close" }),
          origin: "https://evil.example.test",
          source: frame?.contentWindow ?? null,
        }),
      );
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ MessageId: "UI_Close" }),
          origin: "https://office.example.test",
          source: window,
        }),
      );
    });
    expect(goMock).not.toHaveBeenCalled();

    await act(async () => {
      window.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({ MessageId: "UI_Close" }),
          origin: "https://office.example.test",
          source: frame?.contentWindow ?? null,
        }),
      );
    });
    expect(goMock).toHaveBeenCalledWith("/files/fld_b");
  });

  it("returns to the folder the file was opened from when the editor's close button is pressed", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=view&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(apiMock).toHaveBeenCalledWith("POST", "/api/v1/nodes/nod_a1/office", { body: { mode: "view" } }));
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    fireBack();
    expect(goMock).toHaveBeenCalledWith("/files/fld_b");
  });

  it("shows a refusal by its name, not the server's text", async () => {
    answerAll(() => Promise.reject(new ApiError(404, "not_found", {}, 0)));
    render("/edit/nod_a1?mode=edit");
    expect((await screen.findByRole("alert")).textContent).toContain("This file was not found.");
    expect(submit).not.toHaveBeenCalled();
    expect(document.querySelector("form[target='selfcloud-office-editor']")).toBeNull();
  });
});

// fireBack presses the page's Back button.
function fireBack() {
  const back = screen.getByRole("button", { name: /back/i });
  act(() => {
    back.click();
  });
}
