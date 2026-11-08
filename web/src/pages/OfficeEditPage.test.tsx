// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";

// The page's calls to the API and its navigation are replaced: the test checks what the page sends and where it goes.
const apiMock = vi.hoisted(() => vi.fn());
const goMock = vi.hoisted(() => vi.fn());
const createMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../lib/api")>()), api: apiMock }));
// nodesMock answers the page's reads of the file tree by id: the source, and the copy a Save As makes (ef1).
const nodesMock = vi.hoisted(() => ({ byId: new Map<string, unknown>(), children: [] as unknown[], pending: false }));
const announceMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/files/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/files/api")>()),
  createDocument: createMock,
  statNode: (nod: string) => {
    if (nodesMock.pending) return new Promise(() => undefined);
    const n = nodesMock.byId.get(nod);
    return n === undefined ? Promise.reject(new Error(`no node ${nod}`)) : Promise.resolve(n);
  },
  childrenPage: () => Promise.resolve({ items: nodesMock.children }),
}));
vi.mock("../lib/router", () => ({ go: goMock }));
vi.mock("../lib/files/officeSync", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/files/officeSync")>()),
  announceNodeChanged: announceMock,
}));

import OfficeEditPage from "./OfficeEditPage";
import { officeEdit } from "../lib/files/editor";
import { session } from "../lib/session";

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
    announceMock.mockReset();
    nodesMock.byId = new Map([["nod_a1", { ...NODE, name: "letter.docx", parent_id: "nod_dir" }]]);
    nodesMock.children = [];
    nodesMock.pending = false;
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

// ef1: the names, the copy after Save As, the token renewal and the announcements. The editor's messages are sent from its frame.
describe("the editor page follows renames, copies and sessions (ef1)", () => {
  const ORIGIN = "https://office.example.test";
  // A copy is found only when it was made after the request (decision 107): its creation time is now.
  const COPY = {
    id: "nod_c2",
    folder_id: "fld_b",
    parent_id: "nod_dir",
    name: "letter (2).docx",
    kind: "file",
    size: 5,
    created_at: new Date().toISOString(),
    modified_at: "2026-11-08T10:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
  };
  let submit: ReturnType<typeof vi.spyOn>;
  let posted: HTMLFormElement[];

  beforeEach(() => {
    apiMock.mockReset();
    apiMock.mockImplementation((method: string, path: string) => {
      if (method === "POST" && /^\/api\/v1\/nodes\/[a-z0-9_]+\/office$/.test(path)) {
        return Promise.resolve({ url: ACTION, token: TOKEN, expires_at: "2026-11-03T12:00:00Z" });
      }
      return Promise.reject(new Error(`unexpected ${method} ${path}`));
    });
    announceMock.mockReset();
    goMock.mockReset();
    createMock.mockReset();
    officeEdit.set(true);
    nodesMock.byId = new Map([
      ["nod_a1", { ...NODE, name: "letter.docx", parent_id: "nod_dir" }],
      ["nod_c2", COPY],
    ]);
    nodesMock.children = [];
    nodesMock.pending = false;
    posted = [];
    submit = vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(function (this: HTMLFormElement) {
      posted.push(this);
    });
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    window.history.replaceState(null, "", "/");
    session.set(null);
  });

  afterEach(() => {
    session.set(null);
    vi.restoreAllMocks();
  });

  function frame(): HTMLIFrameElement {
    const f = document.querySelector<HTMLIFrameElement>("iframe[name='selfcloud-office-editor']");
    if (f === null) throw new Error("no editor frame");
    return f;
  }

  function from(data: unknown, origin = ORIGIN) {
    act(() => {
      window.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(data), origin, source: frame().contentWindow }));
    });
  }

  async function openEditor(path = "/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b") {
    // The address bar shows the editor's address, as it does in the browser, so a copy's replaced address can be read.
    window.history.replaceState(null, "", path);
    render(path);
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText("Opening the editor…")).toBeNull());
  }

  function officePosts() {
    return apiMock.mock.calls.filter((c) => c[0] === "POST");
  }

  // saveAsAsk opens the Save As dialog from the editor's own request, and confirms it; it returns the frame's spy.
  async function confirmSaveAs(name: string) {
    from({ MessageId: "UI_SaveAs", Values: {} });
    const input = await screen.findByLabelText("File name");
    act(() => {
      fireEvent.change(input, { target: { value: name } });
    });
    const spy = vi.spyOn(frame().contentWindow as Window, "postMessage");
    act(() => {
      screen.getByRole("button", { name: "Save copy" }).click();
    });
    return spy;
  }

  it("F1: a rename in the editor shows the new name in the bar, from the file's own read", async () => {
    await openEditor();
    nodesMock.byId.set("nod_a1", { ...NODE, name: "renamed.docx", parent_id: "nod_dir" });
    from({ MessageId: "File_Rename", Values: { NewName: "renamed" } });
    expect(await screen.findByText("renamed.docx")).toBeTruthy();
    expect(screen.queryByText("letter.docx")).toBeNull();
  });

  it("F1: until the read lands, the bar shows the decoded name the editor sent, with the extension", async () => {
    await openEditor();
    nodesMock.pending = true;
    from({ MessageId: "File_Rename", Values: { NewName: "Quarterly%20report" } });
    expect(await screen.findByText("Quarterly report.docx")).toBeTruthy();
  });

  it("F1: a malformed name in a rename does not throw, and the read's name is shown", async () => {
    await openEditor();
    nodesMock.byId.set("nod_a1", { ...NODE, name: "renamed.docx", parent_id: "nod_dir" });
    const errors: unknown[] = [];
    const onError = (e: ErrorEvent) => errors.push(e.error);
    window.addEventListener("error", onError);
    from({ MessageId: "File_Rename", Values: { NewName: "%E0%A4%A" } });
    expect(await screen.findByText("renamed.docx")).toBeTruthy();
    window.removeEventListener("error", onError);
    expect(errors).toEqual([]);
  });

  it("F1: the Save As dialog suggests the renamed file's base name", async () => {
    await openEditor();
    nodesMock.byId.set("nod_a1", { ...NODE, name: "renamed.docx", parent_id: "nod_dir" });
    from({ MessageId: "File_Rename", Values: { NewName: "renamed" } });
    await screen.findByText("renamed.docx");
    from({ MessageId: "UI_SaveAs", Values: {} });
    expect((await screen.findByLabelText("File name")) as HTMLInputElement).toHaveProperty("value", "renamed");
  });

  it("the Save As request asks for the copy's final name back (Notify)", async () => {
    await openEditor();
    const spy = await confirmSaveAs("letter (2)");
    expect(spy).toHaveBeenCalledTimes(1);
    const sent = JSON.parse(String(spy.mock.calls[0]?.[0])) as { MessageId: string; Values: Record<string, unknown> };
    expect(sent.MessageId).toBe("Action_SaveAs");
    expect(sent.Values).toEqual({ Filename: "letter (2).docx", Notify: true });
  });

  it("F2: Doc_Title arrives first, then Action_Save_Resp: the page moves to the copy, without reopening the editor", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    expect(window.location.pathname).toBe("/edit/nod_c2");
    expect(officePosts()).toHaveLength(1);
    expect(submit).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("letter (2).docx")).toBeTruthy();
  });

  it("F2: when Doc_Title names another file first, Action_Save_Resp's name decides the copy", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "Copy", Title: "Copy - Writer" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_a1");
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
  });

  it("F2: with only Doc_Title arriving, its name finds the copy", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
  });

  it("F2: a name with no exact match in the folder is no guess: the page stays on the source and says where the copy is", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (9)");
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (9).docx" } });
    expect(await screen.findByText("The copy was saved, but it is not shown here. Open its folder to find it.")).toBeTruthy();
    expect(window.location.pathname).toBe("/edit/nod_a1");
    expect(officePosts()).toHaveLength(1);
  });

  // ef1 review (decision 107): a copy is bound only while a Save As this page started is pending, and only when it is the
  // requested name or its free form, made after the request. Outside that, a title or a save answer never moves the page.
  it("no Save As pending: a title naming a sibling does not move the page, and a renewal renews the file the editor opened", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_a1");
    from({ MessageId: "App_TokenExpired", Values: {} });
    await waitFor(() => expect(officePosts()).toHaveLength(2));
    expect(officePosts().every((c) => c[1] === "/api/v1/nodes/nod_a1/office")).toBe(true);
  });

  it("no Save As pending: an answer that names a file is ignored, and nothing is bound", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_a1");
    expect(officePosts()).toHaveLength(1);
  });

  it("Save As pending: an answer naming another file, neither the request nor its free form, is not bound", async () => {
    nodesMock.children = [{ ...COPY, id: "nod_o1", name: "other (2).docx" }];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "other (2).docx", Title: "other (2).docx - Writer" } });
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "other (2).docx" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_a1");
  });

  it("Save As pending: a copy made before the request is not bound", async () => {
    nodesMock.children = [{ ...COPY, created_at: "2026-10-01T00:00:00Z" }];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_a1");
  });

  it("Save As pending: the free form the server gives a taken name is bound (requested letter.docx, made letter (2).docx)", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter");
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
  });

  it("Save As pending: once the answer has come, a later title does not bind", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Action_Save_Resp", Values: { success: true, fileName: "letter (2).docx" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
    // A later title for the sibling, with the window closed, changes nothing more.
    nodesMock.children = [{ ...COPY, id: "nod_o1", name: "other (2).docx" }];
    from({ MessageId: "Doc_Title", Values: { DocName: "other (2).docx", Title: "other (2).docx - Writer" } });
    await new Promise((r) => setTimeout(r, 20));
    expect(window.location.pathname).toBe("/edit/nod_c2");
  });

  it("F4: App_TokenExpired gets a fresh token for the same mode, posted to the editor's origin only", async () => {
    await openEditor();
    from({ MessageId: "App_TokenExpired", Values: {} });
    await waitFor(() => expect(officePosts()).toHaveLength(2));
    expect(officePosts()[1]?.[2]).toEqual({ body: { mode: "edit" } });
    const spy = vi.spyOn(frame().contentWindow as Window, "postMessage");
    from({ MessageId: "App_TokenExpired", Values: {} });
    await waitFor(() => expect(spy).toHaveBeenCalled());
    const [payload, target] = spy.mock.calls[0] as [string, string];
    expect(target).toBe(ORIGIN);
    const sent = JSON.parse(payload) as { MessageId: string; Values: { token: string; ttl: number } };
    expect(sent.MessageId).toBe("Reset_Access_Token");
    expect(sent.Values).toEqual({ token: TOKEN, ttl: Date.parse("2026-11-03T12:00:00Z") });
  });

  it("F4: App_TokenExpiring is answered the same way, before the expiry", async () => {
    await openEditor();
    from({ MessageId: "App_TokenExpiring", Values: { Timeout: 60000 } });
    await waitFor(() => expect(officePosts()).toHaveLength(2));
  });

  it("F4: a view-mode page is renewed in view mode, never in edit", async () => {
    await openEditor("/edit/nod_a1?mode=view&from=%2Ffiles%2Ffld_b");
    from({ MessageId: "App_TokenExpired", Values: {} });
    await waitFor(() => expect(officePosts()).toHaveLength(2));
    expect(officePosts().every((c) => JSON.stringify(c[2]) === JSON.stringify({ body: { mode: "view" } }))).toBe(true);
  });

  it("F4: after a Save As the token is renewed for the copy the editor now has", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_c2"));
    from({ MessageId: "App_TokenExpired", Values: {} });
    await waitFor(() => expect(officePosts().some((c) => c[1] === "/api/v1/nodes/nod_c2/office")).toBe(true));
  });

  it("F4: when the browser session has changed since the editor opened, nothing is minted and the session-ended message is shown", async () => {
    await openEditor();
    session.set({ user: { id: "usr_2" }, csrf: "csrf_other" } as never);
    from({ MessageId: "App_TokenExpired", Values: {} });
    expect(await screen.findByText("The sign-in changed, so this editor can no longer save. Sign in again and reopen the file.")).toBeTruthy();
    expect(officePosts()).toHaveLength(1);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("F4: a renewal that is refused says so, and the status, not the server's text, chooses the words", async () => {
    await openEditor();
    apiMock.mockImplementationOnce(() => Promise.reject(new ApiError(403, "forbidden", {}, 0)));
    from({ MessageId: "App_TokenExpired", Values: {} });
    expect(await screen.findByText("The editor could not renew its session, so the latest changes may not be saved. Reopen the file.")).toBeTruthy();
  });

  it("announces a rename, a copy and a save, and not the load-time state", async () => {
    await openEditor();
    expect(announceMock).not.toHaveBeenCalled();
    from({ MessageId: "Doc_ModifiedStatus", Values: { Modified: false } });
    expect(announceMock).not.toHaveBeenCalled();
    from({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    from({ MessageId: "Doc_ModifiedStatus", Values: { Modified: false } });
    expect(announceMock).toHaveBeenCalledWith("nod_a1", "fld_b", "nod_dir");
    expect(announceMock).toHaveBeenCalledTimes(1);
    from({ MessageId: "Doc_ModifiedStatus", Values: { Modified: true } });
    from({ MessageId: "Action_Save_Resp", Values: { success: true } });
    expect(announceMock).toHaveBeenCalledTimes(2);
  });

  it("announces the copy's parent after a Save As moves the page", async () => {
    nodesMock.children = [COPY];
    await openEditor();
    await confirmSaveAs("letter (2)");
    from({ MessageId: "Doc_Title", Values: { DocName: "letter (2).docx", Title: "letter (2).docx - Writer" } });
    await waitFor(() => expect(announceMock).toHaveBeenCalledWith("nod_c2", "fld_b", "nod_dir"));
  });
});
