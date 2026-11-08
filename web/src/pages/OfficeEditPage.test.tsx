// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";

// The page's calls to the API and its navigation are replaced: the test checks what the page sends and where it goes.
const apiMock = vi.hoisted(() => vi.fn());
const goMock = vi.hoisted(() => vi.fn());
const createMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../lib/api")>()), api: apiMock }));
vi.mock("../lib/files/api", () => ({
  createDocument: createMock,
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
    createMock.mockReset();
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

  // sendFromEditor posts a message from the editor's own frame and origin, as the editor does.
  function sendFromEditor(data: unknown, origin = "https://office.example.test") {
    const frame = document.querySelector<HTMLIFrameElement>("iframe[name='selfcloud-office-editor']");
    act(() => {
      window.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(data), origin, source: frame?.contentWindow ?? null }));
    });
  }

  it("a page a script opened (tab=1) asks the browser to close its tab, and goes to the folder when the tab stays open", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    const closeSpy = vi.spyOn(window, "close").mockImplementation(() => undefined);
    render("/edit/nod_a1?mode=edit&tab=1");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    fireBack();
    expect(closeSpy).toHaveBeenCalledTimes(1);
    await new Promise((r) => setTimeout(r, 250));
    // jsdom leaves window.closed false, which is a tab the browser refused to close: the page then goes to the folder.
    expect(goMock).toHaveBeenCalledWith("/files/fld_b");
  });

  it("a page opened directly closes by going back, and never asks the window to close", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    const closeSpy = vi.spyOn(window, "close").mockImplementation(() => undefined);
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    fireBack();
    expect(closeSpy).not.toHaveBeenCalled();
    expect(goMock).toHaveBeenCalledWith("/files/fld_b");
  });

  it("the bar shows the file's name, a link to its folder in this tab, and the close button", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(screen.getByText("letter.docx")).toBeTruthy();
    expect(screen.getByRole("link", { name: /folder/i }).getAttribute("href")).toBe("/files/fld_b");
    expect(screen.getByRole("button", { name: /close/i })).toBeTruthy();
    // The shell is not drawn here: the page has no header or sidebar of its own.
    expect(document.querySelector("nav")).toBeNull();
  });

  it("the bar shows View only in view mode, and no save state", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=view&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(screen.getByText("View only")).toBeTruthy();
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    expect(screen.getByText("View only")).toBeTruthy();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("shows each save state from the editor's messages, and none before one is known", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("Saved")).toBeNull();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    sendFromEditor({ MessageId: "Action_Save_Resp", Values: { success: true } });
    expect(screen.getByText("Saved")).toBeTruthy();
    // A save that failed is said so; the next change is unsaved again (decision 102, round 2 R4).
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    sendFromEditor({ MessageId: "Action_Save_Resp", Values: { success: false } });
    expect(screen.getByText("Save failed")).toBeTruthy();
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    // A repeated message with the same value keeps the state, and "Saving…" is never shown.
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    expect(screen.queryByText("Saving…")).toBeNull();
  });

  it("ignores a save-state message from another origin", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    sendFromEditor({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } }, "https://evil.example.test");
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  // A GET never makes a file (decision 102, round 2 R5): the old new-document address makes no file and opens no editor.
  it("the address of the old new-document page makes no file and opens no editor", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/new?kind=xlsx&parent=nod_root&from=%2Ffiles%2Ffld_b&tab=1");
    await new Promise((r) => setTimeout(r, 50));
    expect(createMock).not.toHaveBeenCalled();
    expect(submit).not.toHaveBeenCalled();
  });

  it("a reload of the editor's address opens the file and creates nothing", async () => {
    answerAll(() => Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" }));
    render("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b&tab=1");
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    expect(createMock).not.toHaveBeenCalled();
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
  const back = screen.getByRole("button", { name: /close/i });
  act(() => {
    back.click();
  });
}
