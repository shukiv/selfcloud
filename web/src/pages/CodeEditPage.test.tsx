// SPDX-License-Identifier: AGPL-3.0-or-later

import "../test/rangeRects";
import { act, fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { EditorView } from "@codemirror/view";
import { forEachDiagnostic } from "@codemirror/lint";
import { createMemoryRouter, RouterProvider, useNavigate, type NavigateFunction } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const goMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/router", () => ({ go: goMock, signInURL: () => "/login" }));

import CodeEditPage from "./CodeEditPage";
import { session } from "../lib/session";

// The page's meta nonce, as the server writes it: CodeMirror's style sheet is written with it.
const meta = document.createElement("meta");
meta.setAttribute("property", "csp-nonce");
meta.setAttribute("nonce", "nonce-for-tests");
document.head.appendChild(meta);

const NOD = "nod_a1";
const NOD_B = "nod_b2";
const NOD_DIR = "nod_dir";
const FLD = "fld_b";
const ETAG = '"0123abcd"';
const ETAG_B = '"bbbb"';
const MiB = 1024 * 1024;
const BINARY_NOTICE = "This file is not text, so it cannot be edited here. You can download it.";
const TOO_LARGE_NOTICE = "This file is too large to edit here. You can download it.";
const NOT_UTF8_NOTICE = "This file is not UTF-8 text. Characters that could not be read show as �.";

interface Options {
  role?: string;
  body?: Uint8Array;
  size?: number;
  mime?: string;
  name?: string;
  noDownload?: boolean;
  // nested puts the file in a folder under the root, so a copy's path carries that folder's name (design 4.4).
  nested?: boolean;
  // taken lists full paths a copy finds taken, before the copies the test makes (for example "/a (2).json").
  taken?: string[];
  // put answers a save of the first file, instead of the server's own rule (the If-Match check runs first).
  put?: (init: RequestInit) => Response | Promise<Response>;
  // post answers a copy (a POST to a folder's files) when it returns a response; undefined leaves the copy to the server.
  post?: (init: RequestInit) => Response | Promise<Response> | undefined;
  // read answers the content GET of the first file with this status and the error envelope (a refused open).
  read?: number;
  // hold makes the content GET of the first file wait for the gate while on, so a test finishes an answer after the page moved.
  hold?: { on: boolean; gate: Promise<void> };
}

interface Stored {
  id: string;
  name: string;
  kind?: "file" | "dir";
  parent?: string | null;
  etag: string;
  body: Uint8Array;
  mime: string;
  size?: number;
  role: string;
  noDownload: boolean;
  put?: (init: RequestInit) => Response | Promise<Response>;
}

// server replaces fetch for one test: two files (NOD, the one the test opens, and NOD_B, for a move), the save that checks
// If-Match against the file's current ETag as the server does, and a record of every request and of every save's answer.
function server(o: Options = {}) {
  const calls: Array<{ method: string; url: string; init: RequestInit }> = [];
  const answers: number[] = [];
  let seq = 0;
  // The folder's root is nod_root (no parent); the file sits in it, or, when nested, in a folder named docs under it.
  const files: Record<string, Stored> = {
    nod_root: { id: "nod_root", name: "", kind: "dir", parent: null, etag: '"root"', body: new Uint8Array(0), mime: "", role: "editor", noDownload: false },
    [NOD_DIR]: { id: NOD_DIR, name: "docs", kind: "dir", parent: "nod_root", etag: '"dir"', body: new Uint8Array(0), mime: "", role: "editor", noDownload: false },
    [NOD]: {
      id: NOD,
      name: o.name ?? "a.json",
      parent: o.nested ? NOD_DIR : "nod_root",
      etag: ETAG,
      body: o.body ?? new TextEncoder().encode("{}"),
      mime: o.mime ?? "application/json",
      size: o.size,
      role: o.role ?? "editor",
      noDownload: o.noDownload ?? false,
      put: o.put,
    },
    [NOD_B]: {
      id: NOD_B,
      name: "b.json",
      etag: ETAG_B,
      body: new TextEncoder().encode("{}"),
      mime: "application/json",
      role: "editor",
      noDownload: false,
    },
  };
  // The full paths taken in the folder: the file the test opens, and any the test names.
  const takenPaths = new Set<string>([o.nested ? "/docs/a.json" : "/a.json", ...(o.taken ?? [])]);
  const nodeJSON = (f: Stored) => ({
    id: f.id,
    folder_id: "fld_b",
    parent_id: f.parent ?? null,
    name: f.name,
    kind: f.kind ?? "file",
    size: f.size ?? f.body.length,
    mime_type: f.mime,
    etag: f.etag,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: f.role,
    no_download: f.noDownload,
    editors_can_share: false,
  });
  const f = vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? "GET";
    calls.push({ method, url, init });
    // A copy (design 4.4): POST /api/v1/folders/{fld}/files?path=..., refused with 412 when If-None-Match is * and the name is taken.
    const post = /^\/api\/v1\/folders\/([^/?]+)\/files\?path=(.*)$/.exec(url);
    if (method === "POST" && post !== null) {
      if (o.post) {
        const answer = await o.post(init);
        if (answer !== undefined) return answer;
      }
      const path = new URL(url, "http://localhost").searchParams.get("path") ?? "";
      const headers = (init.headers ?? {}) as Record<string, string>;
      if (headers["If-None-Match"] === "*" && takenPaths.has(path)) {
        return Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
      }
      const id = `nod_copy${++seq}`;
      takenPaths.add(path);
      files[id] = {
        id,
        name: path.slice(path.lastIndexOf("/") + 1),
        parent: "nod_root",
        etag: `"copy-${seq}"`,
        body: init.body as Uint8Array,
        mime: headers["Content-Type"] ?? "application/octet-stream",
        role: "editor",
        noDownload: false,
      };
      return Response.json(nodeJSON(files[id]!), { status: 201 });
    }
    const m = /^\/api\/v1\/nodes\/([^/?]+)(\/content)?(\?.*)?$/.exec(url);
    const file = m ? files[m[1]!] : undefined;
    if (m === null || file === undefined) return new Response(null, { status: 404 });
    // HEAD of the content gives the current ETag and records no view (design 4.4).
    if (method === "HEAD" && m[2] !== undefined) return new Response(null, { status: 200, headers: { ETag: file.etag } });
    if (method === "GET" && m[2] === undefined) return Response.json(nodeJSON(file));
    if (method === "GET") {
      if (file.id === NOD && o.hold?.on) await o.hold.gate;
      if (file.id === NOD && o.read !== undefined) {
        return Response.json({ error: { code: "unauthenticated", details: {} } }, { status: o.read });
      }
      return new Response(file.body as unknown as BodyInit, { status: 200, headers: { ETag: file.etag } });
    }
    if (method === "PUT" && m[2] !== undefined) {
      const headers = (init.headers ?? {}) as Record<string, string>;
      // The server's own rule: the save must name the file's current ETag, or it is refused.
      if (headers["If-Match"] !== file.etag) {
        answers.push(412);
        return Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
      }
      if (file.put) {
        const r = await file.put(init);
        answers.push(r.status);
        return r;
      }
      file.etag = `"etag-${++seq}"`;
      file.body = init.body as Uint8Array;
      answers.push(200);
      return Response.json(nodeJSON(file));
    }
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", f);
  // touch is another client's change to the open file: a new ETag and new bytes, so the page's next save is refused.
  const touch = (body: string) => {
    files[NOD]!.etag = `"touched-${++seq}"`;
    files[NOD]!.body = new TextEncoder().encode(body);
  };
  return {
    calls,
    answers,
    files,
    touch,
    puts: () => calls.filter((c) => c.method === "PUT"),
    copies: () => calls.filter((c) => c.method === "POST"),
  };
}

// held makes a save that waits until the test lets it answer: the answer is a save of the file, with the ETag it was given.
function held(etag = '"held"') {
  let release: () => void = () => undefined;
  const gate = new Promise<void>((r) => (release = r));
  const put = async () => {
    await gate;
    return Response.json({ id: NOD, etag, mime_type: "application/json" });
  };
  return { put, release };
}

// navigateTo moves the page to another address of the app, as a link does, without leaving the page's mount.
let navigateTo: NavigateFunction | null = null;
function Grab() {
  navigateTo = useNavigate();
  return null;
}

// The page runs under a data router, as the app does: the unsaved guard is a router blocker (useBlocker), which needs one.
let router: ReturnType<typeof createMemoryRouter> | null = null;
function render(path = `/edit/${NOD}?mode=edit`) {
  router = createMemoryRouter(
    [
      { path: "/edit/:nod", element: <><Grab /><CodeEditPage /></> },
      { path: "/files/:fld", element: <p>files</p> },
    ],
    { initialEntries: [path] },
  );
  return rtlRender(<RouterProvider router={router} />);
}

// goTo moves the test's router to an address, as a link does. The navigation is not awaited: the unsaved guard may hold it (the
// navigation then waits for the person's choice, which the test makes), and a navigation left pending stops the next test's first
// render. The act lets an unblocked navigation settle inside the test.
async function goTo(path: string) {
  await act(async () => {
    void router!.navigate(path);
    await pause();
  });
}

const content = () => document.querySelector<HTMLElement>(".cm-content")!;
const view = () => EditorView.findFromDOM(content())!;
// type inserts text at a position, as a key press does: CodeMirror's own input path is not run by jsdom.
function type(at: number, text: string) {
  view().dispatch({ changes: { from: at, insert: text } });
}
// ctrlS presses Ctrl+S in the editor and returns whether the browser's default was left alone (false: prevented).
function ctrlS() {
  return fireEvent.keyDown(content(), { key: "s", code: "KeyS", ctrlKey: true });
}
const pause = () => new Promise((r) => setTimeout(r, 20));

beforeEach(() => {
  goMock.mockClear();
  // go moves the router, so Close and Back are subject to the same unsaved guard as a link (design 4.6).
  goMock.mockImplementation((url: string) => navigateTo?.(url));
  navigateTo = null;
  // A signed-in browser session: the reads and the save are made under it.
  session.set({ user: { id: "usr_1", role: "user" }, csrf: "csrf_test" } as never);
});

afterEach(() => {
  vi.unstubAllGlobals();
  session.set(null);
  // Each test's data router is stopped, so no navigation of one test reaches the next.
  router?.dispose();
  router = null;
});

describe("the editor page", () => {
  it("reads the whole file with no Range, shows its text, and takes the focus in the editor", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    const get = s.calls.find((c) => c.method === "GET" && c.url.includes("/content"));
    expect(get?.init.headers).toBeUndefined();
    expect(document.activeElement && content().contains(document.activeElement)).toBe(true);
  });

  it("gives an editor a Save button with its tooltip's name, and shows Saved after a save", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    expect(await screen.findByText("Unsaved changes")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    expect(s.puts()).toHaveLength(1);
  });

  it("saves with Ctrl+S once, with If-Match and the stored type, and stops the browser's own save", async () => {
    const s = server({ mime: "application/json; charset=utf-8" });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    const stillDefault = ctrlS();
    expect(stillDefault).toBe(false);
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    const init = s.puts()[0]!.init;
    expect(init.headers).toMatchObject({ "If-Match": ETAG, "Content-Type": "application/json; charset=utf-8" });
    expect(new TextDecoder().decode(init.body as Uint8Array)).toBe("x{}");
  });

  it("Ctrl+S typed in the find panel saves once, and stops the browser's own save (decision 124, the find panel)", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    fireEvent.keyDown(content(), { key: "f", code: "KeyF", ctrlKey: true });
    const input = await waitFor(() => {
      const el = document.querySelector<HTMLInputElement>('.cm-search input[name="search"]');
      expect(el).not.toBeNull();
      return el!;
    });
    const stillDefault = fireEvent.keyDown(input, { key: "s", code: "KeyS", ctrlKey: true });
    expect(stillDefault).toBe(false);
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    expect(new TextDecoder().decode(s.puts()[0]!.init.body as Uint8Array)).toBe("x{}");
  });

  it("saves the bytes a file had: a byte order mark and CRLF come back as they were", async () => {
    const original = new Uint8Array([0xef, 0xbb, 0xbf, 0x7b, 0x0d, 0x0a, 0x22, 0x61, 0x22, 0x0d, 0x0a, 0x7d]);
    const s = server({ body: original });
    render();
    await waitFor(() => expect(content().textContent).toContain('"a"'));
    type(1, "x");
    ctrlS();
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    const sent = new Uint8Array(s.puts()[0]!.init.body as ArrayBuffer);
    expect([...sent]).toEqual([0xef, 0xbb, 0xbf, 0x7b, 0x78, 0x0d, 0x0a, 0x22, 0x61, 0x22, 0x0d, 0x0a, 0x7d]);
  });

  it("a second save sends the ETag the first save's answer gave, and the server accepts it", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    await waitFor(() => expect(s.answers).toEqual([200]));
    type(0, "y");
    ctrlS();
    await waitFor(() => expect(s.answers).toEqual([200, 200]));
    expect(s.puts()[1]!.init.headers).toMatchObject({ "If-Match": "\"etag-1\"" });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("does not save a file that has not changed", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    ctrlS();
    await pause();
    expect(s.puts()).toHaveLength(0);
  });

  // A navigation to the same address (a refetch, a re-render of the route, a link to the page it is on) must not open the
  // file again: that would read the server's text over the person's typed text. The page's open effect is keyed by the address.
  it("a navigation to the same address keeps the typed text and does not read the file again", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    await goTo(`/edit/${NOD}?mode=edit`);
    expect(content().textContent).toContain("x{}");
    expect(s.calls.filter((c) => c.method === "GET" && c.url.includes("/content"))).toHaveLength(1);
  });

  it("a second Ctrl+S while the first save is in flight sends no second PUT", async () => {
    const hold = held();
    const s = server({ put: hold.put });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    ctrlS();
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    await pause();
    expect(s.puts()).toHaveLength(1);
    await act(async () => hold.release());
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    expect(s.puts()).toHaveLength(1);
  });

  it("a save answered 403 makes the page read-only, with its message, and keeps the text", async () => {
    const s = server({ put: () => Response.json({ error: { code: "forbidden", details: {} } }, { status: 403 }) });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("You can no longer save this file. The text stays here so you can copy it.")).toBeTruthy();
    await waitFor(() => expect(content().getAttribute("aria-readonly")).toBe("true"));
    expect(content().textContent).toContain("x{}");
    ctrlS();
    await pause();
    expect(s.puts()).toHaveLength(1);
  });

  it("a save answered 404 makes the page read-only, with its message, and keeps the text", async () => {
    const s = server({ put: () => Response.json({ error: { code: "not_found", details: {} } }, { status: 404 }) });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("This file was deleted. The text stays here so you can copy it.")).toBeTruthy();
    await waitFor(() => expect(content().getAttribute("aria-readonly")).toBe("true"));
    expect(content().textContent).toContain("x{}");
    ctrlS();
    await pause();
    expect(s.puts()).toHaveLength(1);
  });

  it("refuses to send a draft over 2 MiB, with no request, and says why", async () => {
    const s = server({ body: new Uint8Array(2 * MiB - 4).fill(0x61) });
    render();
    await waitFor(() => expect(content().textContent?.length).toBeGreaterThan(1000));
    type(0, "xxxxxxxxxx");
    ctrlS();
    expect(await screen.findByText("The file is too large to save here.")).toBeTruthy();
    expect(s.puts()).toHaveLength(0);
  });

  it("the Save button stays enabled while a save is in flight, and keeps the focus after the save", async () => {
    const hold = held();
    server({ put: hold.put });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    const save = screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    save.focus();
    fireEvent.click(save);
    await waitFor(() => expect(screen.getByText("Saving…")).toBeTruthy());
    expect(save.disabled).toBe(false);
    await act(async () => hold.release());
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    expect(document.activeElement).toBe(save);
  });

  it("a save that answers after the page moved to another file does not touch the new file", async () => {
    const hold = held('"a-new"');
    const s = server({ put: hold.put });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    await goTo(`/edit/${NOD_B}?mode=edit`);
    // The save is still in flight, so the unsaved guard asks first: the person chooses to leave, and the answer then finds the old file.
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Leave" }));
    await waitFor(() => expect(document.querySelector('.cm-content[aria-label="b.json"]')).not.toBeNull());
    await act(async () => hold.release());
    await pause();
    type(0, "y");
    ctrlS();
    await waitFor(() => expect(s.puts().filter((c) => c.url.includes(NOD_B))).toHaveLength(1));
    const toB = s.puts().find((c) => c.url.includes(NOD_B))!;
    expect(toB.init.headers).toMatchObject({ "If-Match": ETAG_B });
  });

  it("a contributor who opens the editor by its address sees View only and no Save", async () => {
    const s = server({ role: "contributor" });
    render();
    expect(await screen.findByText("View only")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    await waitFor(() => expect(content().getAttribute("aria-readonly")).toBe("true"));
    // Ctrl+S is still taken (the browser's save dialog does not open), and nothing is sent.
    expect(ctrlS()).toBe(false);
    await pause();
    expect(s.puts()).toHaveLength(0);
  });

  it("a file that is not UTF-8 opens read-only, with its notice, and a save is not sent", async () => {
    const s = server({ body: new Uint8Array([0x63, 0x61, 0x66, 0xe9, 0x0a]) });
    render();
    await screen.findByText(NOT_UTF8_NOTICE);
    await waitFor(() => expect(content().getAttribute("aria-readonly")).toBe("true"));
    ctrlS();
    await pause();
    expect(s.puts()).toHaveLength(0);
  });

  it("a file with a NUL byte past its first 8 KiB is not opened for editing: the binary notice, Download, and no save", async () => {
    const body = new Uint8Array(9002).fill(0x61);
    body[9000] = 0;
    const s = server({ body });
    render();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(BINARY_NOTICE)).toBeTruthy();
    expect(within(alert).getByRole("button", { name: "Download" })).toBeTruthy();
    expect(document.querySelector(".cm-content")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(s.puts()).toHaveLength(0);
  });

  it("a file that starts with a NUL byte is not opened for editing either", async () => {
    const body = new Uint8Array([0x61, 0x00, 0x61]);
    server({ body });
    render();
    expect(await screen.findByText(BINARY_NOTICE)).toBeTruthy();
    expect(document.querySelector(".cm-content")).toBeNull();
  });

  it("a file of exactly 2 MiB opens for editing", async () => {
    const body = new Uint8Array(2 * MiB).fill(0x61);
    for (let i = 99; i < body.length; i += 100) body[i] = 0x0a;
    server({ body });
    render();
    await waitFor(() => expect(document.querySelector(".cm-content")).not.toBeNull());
    expect(screen.queryByText(TOO_LARGE_NOTICE)).toBeNull();
  });

  it("a file one byte over 2 MiB does not open for editing: the notice, and no read of it", async () => {
    const body = new Uint8Array(2 * MiB + 1).fill(0x61);
    for (let i = 99; i < body.length; i += 100) body[i] = 0x0a;
    const s = server({ body });
    render();
    expect(await screen.findByText(TOO_LARGE_NOTICE)).toBeTruthy();
    expect(document.querySelector(".cm-content")).toBeNull();
    expect(s.calls.some((c) => c.url.includes("/content"))).toBe(false);
  });

  it("on the not-UTF-8 notice a no-download viewer sees no Download, and a contributor does", async () => {
    const bad = new Uint8Array([0x63, 0x61, 0x66, 0xe9, 0x0a]);
    server({ role: "viewer", noDownload: true, body: bad });
    const first = render();
    const notice = await screen.findByText(NOT_UTF8_NOTICE);
    expect(within(notice.closest(".ant-alert") as HTMLElement).queryByRole("button", { name: "Download" })).toBeNull();
    first.unmount();
    server({ role: "contributor", noDownload: true, body: bad });
    render();
    const again = await screen.findByText(NOT_UTF8_NOTICE);
    expect(within(again.closest(".ant-alert") as HTMLElement).getByRole("button", { name: "Download" })).toBeTruthy();
  });

  it("Escape with no find panel open does nothing: the page is not left", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    fireEvent.keyDown(content(), { key: "Escape" });
    expect(goMock).not.toHaveBeenCalled();
    expect(document.querySelector(".cm-editor")).not.toBeNull();
  });

  it("Escape closes the find panel first", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    fireEvent.keyDown(content(), { key: "f", code: "KeyF", ctrlKey: true });
    await waitFor(() => expect(document.querySelector(".cm-search")).not.toBeNull());
    fireEvent.keyDown(document.querySelector(".cm-search")!, { key: "Escape" });
    await waitFor(() => expect(document.querySelector(".cm-search")).toBeNull());
    expect(goMock).not.toHaveBeenCalled();
  });

  it("keeps the code area left to right in a right-to-left page, with the gutter before the text", async () => {
    server();
    document.documentElement.dir = "rtl";
    try {
      render();
      await waitFor(() => expect(content().textContent).toContain("{}"));
      const host = document.querySelector<HTMLElement>('[data-testid="code-editor"]')!;
      expect(host.getAttribute("dir")).toBe("ltr");
      const order = [...document.querySelector(".cm-scroller")!.children].map((c) => c.className);
      expect(order.findIndex((c) => c.includes("cm-gutters"))).toBeLessThan(order.findIndex((c) => c.includes("cm-content")));
    } finally {
      document.documentElement.dir = "ltr";
    }
  });

  it("writes the editor's style sheets with the page's nonce, the CSP's", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    const sheets = [...document.querySelectorAll("style")].filter((s) => (s.textContent ?? "").includes(".cm-scroller"));
    expect(sheets.length).toBeGreaterThan(0);
    for (const s of sheets) expect(s.getAttribute("nonce")).toBe("nonce-for-tests");
  });

  it("takes the focus into the code area when the page opens", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    expect(document.activeElement && content().contains(document.activeElement)).toBe(true);
  });
});

// The dialog's footer words and the guard's, as the English catalogue has them (lib/code/text.ts, decision 130).
const RELOAD = "Reload theirs";
const COPY = "Keep as a copy";
const OVERWRITE = "Overwrite theirs with mine (theirs stays in history)";
const KEEP = "Keep my draft (saving will ask again)";
const MIXED = "This file mixes line break styles. Saving writes the first style on every line.";
const MAYBE = "The last save may have reached the server before the connection failed.";
const FAILED = "Save failed. The text stays here, so try again.";
const MESSAGE = "This file changed since you opened it. Reload it to see the saved version.";
// The new notices of the review round 1 fixes (decision 130): the sign-in that ended, a copy refused by name or folder, a full store.
const SIGNED_OUT = "Your sign-in has ended. The text stays here. Sign in again to save it.";
const BAD_NAME = "This name is too long or not allowed in this folder. Rename the copy and try again.";
const FOLDER_GONE = "The folder for this copy is gone. The text stays here so you can copy it.";
const STORAGE_FULL = "The storage is full. The text stays here, so free some space and try again.";

// openConflict opens the file, types a draft, and saves it after another client changed the file: the save is refused with 412,
// and the conflict dialog opens (design 4.4).
async function openConflict(s: ReturnType<typeof server>) {
  render();
  await waitFor(() => expect(content().textContent).toContain("{}"));
  s.touch("changed by someone else\n");
  type(0, "x");
  ctrlS();
  return screen.findByRole("dialog");
}

// modalWith is the open modal whose text has this words, or null. A closing modal (its leave motion) is not open.
const modalWith = (text: string) =>
  [...document.querySelectorAll<HTMLElement>(".ant-modal")].find((m) => (m.textContent ?? "").includes(text) && !m.className.includes("ant-zoom-leave")) ?? null;
const dialogHas = (text: string) => modalWith(text) !== null;

// dialogOpen says a modal is open. jsdom never ends an antd motion, so a dialog that is closing keeps its leave classes: it is
// closed, though it stays in the page.
const dialogOpen = () => {
  const m = document.querySelector(".ant-modal");
  return m !== null && !m.className.includes("ant-zoom-leave");
};

// postPaths is the path each copy was sent to, decoded.
const postPaths = (s: ReturnType<typeof server>) => s.copies().map((c) => new URL(c.url, "http://localhost").searchParams.get("path"));

describe("the editor's conflict dialog, copies, guards and lint (decision 130)", () => {
  it("a save the server refuses with 412 opens the dialog with its four choices, and reads the file's ETag by HEAD", async () => {
    const s = server();
    const d = await openConflict(s);
    for (const name of [RELOAD, COPY, OVERWRITE, KEEP]) expect(within(d).getByRole("button", { name })).toBeTruthy();
    expect(content().textContent).toContain("x{}");
    expect(s.calls.some((c) => c.method === "HEAD" && c.url === `/api/v1/nodes/${NOD}/content`)).toBe(true);
    // The check records no view: the only GET of the content is the one that opened the page.
    expect(s.calls.filter((c) => c.method === "GET" && c.url.includes("/content"))).toHaveLength(1);
  });

  it("after a save whose answer was lost, a conflict says the draft may already be on the server (decision 124, LOW-4)", async () => {
    // The first save's request fails with no answer; the server may still have stored the draft. The next save is refused with 412.
    let attempt = 0;
    const s = server({
      put: () => {
        if (attempt++ === 0) throw new TypeError("connection lost");
        return Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
      },
    });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    await screen.findByText("Save failed. The text stays here, so try again.");
    ctrlS();
    const d = await screen.findByRole("dialog");
    expect(within(d).getByText(MAYBE)).toBeTruthy();
    expect(s.puts()).toHaveLength(2);
  });

  it("Keep my draft closes the dialog with no request, and the next save is refused and asks again", async () => {
    const s = server();
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: KEEP }));
    await waitFor(() => expect(dialogOpen()).toBe(false));
    expect(content().textContent).toContain("x{}");
    expect(s.puts()).toHaveLength(1);
    ctrlS();
    await screen.findByRole("dialog");
    expect(s.puts()).toHaveLength(2);
  });

  it("Reload theirs asks first, then shows the saved version", async () => {
    const s = server();
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: RELOAD }));
    const pop = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-popover:not(.ant-popover-hidden)");
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(within(pop).getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(content().textContent).toContain("changed by someone else"));
    await waitFor(() => expect(dialogOpen()).toBe(false));
  });

  it("Keep as a copy saves the draft under the next free name beside the file, and leaves the original as it was", async () => {
    const s = server();
    const d = await openConflict(s);
    const original = new Uint8Array(s.files[NOD]!.body);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText("Saved as a (2).json")).toBeTruthy();
    expect(postPaths(s)).toEqual(["/a (2).json"]);
    const sent = s.copies()[0]!;
    expect(sent.init.headers).toMatchObject({ "If-None-Match": "*", "Content-Type": "application/json" });
    expect(new TextDecoder().decode(sent.init.body as Uint8Array)).toBe("x{}");
    expect([...s.files[NOD]!.body]).toEqual([...original]);
  });

  it("Keep as a copy skips a name that is taken and takes the next one", async () => {
    const s = server({ taken: ["/a (2).json", "/a (3).json"] });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText("Saved as a (4).json")).toBeTruthy();
    expect(postPaths(s)).toEqual(["/a (2).json", "/a (3).json", "/a (4).json"]);
  });

  it("Keep as a copy tries twenty names at most, then says it could not find a free name", async () => {
    const taken = Array.from({ length: 30 }, (_, i) => `/a (${i + 2}).json`);
    const s = server({ taken });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await within(await screen.findByRole("dialog")).findByText("Could not find a free name.")).toBeTruthy();
    expect(postPaths(s)).toHaveLength(20);
  });

  it("a copy of a file in a folder goes into that folder, its path read from the parent chain", async () => {
    const s = server({ nested: true });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText("Saved as a (2).json")).toBeTruthy();
    expect(postPaths(s)).toEqual(["/docs/a (2).json"]);
  });

  it("Overwrite saves the draft over the changed file with the ETag the check read", async () => {
    const s = server();
    const d = await openConflict(s);
    const touched = s.files[NOD]!.etag;
    fireEvent.click(within(d).getByRole("button", { name: OVERWRITE }));
    await waitFor(() => expect(dialogOpen()).toBe(false));
    expect(await screen.findByText("Saved")).toBeTruthy();
    const last = s.puts()[1]!;
    expect(last.init.headers).toMatchObject({ "If-Match": touched });
    expect(new TextDecoder().decode(s.files[NOD]!.body)).toBe("x{}");
  });

  it("Overwrite that meets another change opens the dialog again, with a fresh check", async () => {
    let refused = 0;
    const s = server({
      put: () => {
        // The first matched save is refused: another client changed the file again after the check.
        if (refused++ === 0) return Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
        return Response.json({ id: NOD, etag: '"after"', mime_type: "application/json" });
      },
    });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: OVERWRITE }));
    await waitFor(() => expect(s.calls.filter((c) => c.method === "HEAD")).toHaveLength(2));
    const again = await screen.findByRole("dialog");
    fireEvent.click(within(again).getByRole("button", { name: OVERWRITE }));
    await waitFor(() => expect(dialogOpen()).toBe(false));
    expect(s.puts()).toHaveLength(3);
  });

  it("leaving with unsaved text asks first, and Stay keeps the page and the text", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    // The guard reads the save state from the last render: the unsaved state is drawn first, then the navigation is made.
    await screen.findByText("Unsaved changes");
    await goTo(`/files/${FLD}`);
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Stay" }));
    await waitFor(() => expect(dialogOpen()).toBe(false));
    expect(router!.state.location.pathname).toBe(`/edit/${NOD}`);
    expect(content().textContent).toContain("x{}");
    expect(s.puts()).toHaveLength(0);
  });

  it("Leave goes on to the other address without saving", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    // The guard reads the save state from the last render: the unsaved state is drawn first, then the navigation is made.
    await screen.findByText("Unsaved changes");
    await goTo(`/files/${FLD}`);
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Leave" }));
    await waitFor(() => expect(router!.state.location.pathname).toBe(`/files/${FLD}`));
    expect(s.puts()).toHaveLength(0);
  });

  it("Save and leave saves the draft first, then goes on", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    // The guard reads the save state from the last render: the unsaved state is drawn first, then the navigation is made.
    await screen.findByText("Unsaved changes");
    await goTo(`/files/${FLD}`);
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save and leave" }));
    await waitFor(() => expect(router!.state.location.pathname).toBe(`/files/${FLD}`));
    expect(s.puts()).toHaveLength(1);
    expect(new TextDecoder().decode(s.puts()[0]!.init.body as Uint8Array)).toBe("x{}");
  });

  it("Save and leave that does not save stays on the page, and the conflict dialog shows", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    s.touch("changed by someone else\n");
    await screen.findByText("Unsaved changes");
    await goTo(`/files/${FLD}`);
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save and leave" }));
    // The guard's dialog closes as the conflict's opens, so the conflict's own button is found on the whole page.
    expect(await screen.findByRole("button", { name: RELOAD })).toBeTruthy();
    expect(router!.state.location.pathname).toBe(`/edit/${NOD}`);
  });

  it("Close with unsaved text asks first, as a link does", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    await screen.findByText("Unsaved changes");
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Stay" }));
    await waitFor(() => expect(router!.state.location.pathname).toBe(`/edit/${NOD}`));
  });

  it("a navigation to the same address does not ask", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    await goTo(`/edit/${NOD}?mode=edit`);
    await pause();
    expect(dialogOpen()).toBe(false);
  });

  it("beforeunload is cancelled while the text is unsaved, and not once it is saved", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    const clean = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
    type(0, "x");
    await screen.findByText("Unsaved changes");
    const dirty = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirty);
    expect(dirty.defaultPrevented).toBe(true);
    ctrlS();
    await screen.findByText("Saved");
    const after = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(after);
    expect(after.defaultPrevented).toBe(false);
  });

  it("a file with mixed line breaks shows a notice, and a save writes its first style on every line", async () => {
    const s = server({ body: new TextEncoder().encode("a\r\nb\nc\r\n") });
    render();
    expect(await screen.findByText(MIXED)).toBeTruthy();
    await waitFor(() => expect(content().textContent).toContain("a"));
    type(0, "x");
    ctrlS();
    await waitFor(() => expect(s.puts()).toHaveLength(1));
    expect(new TextDecoder().decode(s.puts()[0]!.init.body as Uint8Array)).toBe("xa\r\nb\r\nc\r\n");
  });

  it("a JSON file shows its syntax problems as markers and a count, and still saves", async () => {
    const s = server({ body: new TextEncoder().encode('{\n  "a": 1,\n}\n') });
    render();
    await waitFor(() => expect(document.querySelector(".cm-lint-marker")).not.toBeNull());
    expect(screen.getByTestId("lint-count").textContent).toMatch(/^\d+ problems?$/);
    type(0, "x");
    ctrlS();
    await waitFor(() => expect(s.puts()).toHaveLength(1));
  });

  it("a file with a line past 2000 characters opens with wrapping off (design 3.2), and the wrap button still turns it on", async () => {
    server({ body: new TextEncoder().encode(`{"a":"${"x".repeat(2500)}"}\n`) });
    render();
    await waitFor(() => expect(content().textContent).toContain('"a"'));
    expect(content().classList.contains("cm-lineWrapping")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Wrap lines" }));
    await waitFor(() => expect(content().classList.contains("cm-lineWrapping")).toBe(true));
  });

  // ---- The review round 1 fixes (decision 130, the lead's findings). Each test is red before its fix.

  it("a double click on Keep as a copy sends one copy, not two (MED-1)", async () => {
    const s = server();
    const d = await openConflict(s);
    // Two native clicks in one task: React has not yet rendered the busy state between them, so only the ref guard stops the second.
    const copy = within(d).getByRole("button", { name: COPY });
    copy.click();
    copy.click();
    expect(await screen.findByText("Saved as a (2).json")).toBeTruthy();
    await pause();
    expect(postPaths(s)).toEqual(["/a (2).json"]);
  });

  it("a double click on Overwrite saves once (MED-1)", async () => {
    const s = server();
    const d = await openConflict(s);
    const over = within(d).getByRole("button", { name: OVERWRITE });
    over.click();
    over.click();
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    await pause();
    // One save from Ctrl+S, which met the change, and one overwrite.
    expect(s.puts()).toHaveLength(2);
  });

  it("an overwrite that gets no answer marks the next conflict as possibly saved (MED-2)", async () => {
    // The overwrite's save gets no answer; the next save is refused with 412, so its dialog must say the draft may be saved.
    let attempt = 0;
    const s = server({
      put: () => {
        if (attempt++ === 0) throw new TypeError("connection lost");
        return Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
      },
    });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: OVERWRITE }));
    expect(await screen.findByText(FAILED)).toBeTruthy();
    await pause();
    ctrlS();
    await waitFor(() => expect(dialogHas(MAYBE)).toBe(true));
  });

  it("a copy that gets no answer marks the next conflict as possibly saved (MED-2)", async () => {
    const s = server({
      post: () => {
        throw new TypeError("connection lost");
      },
    });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText(FAILED)).toBeTruthy();
    ctrlS();
    await waitFor(() => expect(dialogHas(MAYBE)).toBe(true));
  });

  it("an overwrite that is answered clears the possibly-saved mark (MED-2)", async () => {
    let calls = 0;
    const s = server({
      put: () => {
        calls++;
        // Only the overwrites reach this: a save refused by the If-Match check never does.
        if (calls === 1) throw new TypeError("connection lost");
        return Response.json({
          id: NOD,
          folder_id: FLD,
          parent_id: "nod_root",
          name: "a.json",
          kind: "file",
          size: 2,
          mime_type: "application/json",
          etag: '"after"',
          created_at: "2026-10-01T00:00:00Z",
          modified_at: "2026-10-01T00:00:00Z",
          role: "editor",
          no_download: false,
          editors_can_share: false,
        });
      },
    });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: OVERWRITE }));
    expect(await screen.findByText(FAILED)).toBeTruthy();
    ctrlS();
    await waitFor(() => expect(dialogHas(MAYBE)).toBe(true));
    fireEvent.click(within(modalWith(MAYBE)!).getByRole("button", { name: OVERWRITE }));
    await waitFor(() => expect(screen.getByText("Saved")).toBeTruthy());
    s.touch("changed again\n");
    type(0, "y");
    ctrlS();
    await waitFor(() => expect(dialogOpen()).toBe(true));
    await waitFor(() => expect(dialogHas(MESSAGE)).toBe(true));
    expect(dialogHas(MAYBE)).toBe(false);
  });

  it("a copy that is answered clears the possibly-saved mark (MED-2)", async () => {
    let calls = 0;
    const s = server({
      post: () => {
        if (calls++ === 0) throw new TypeError("connection lost");
        return undefined;
      },
    });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText(FAILED)).toBeTruthy();
    ctrlS();
    await waitFor(() => expect(dialogHas(MAYBE)).toBe(true));
    fireEvent.click(within(modalWith(MAYBE)!).getByRole("button", { name: COPY }));
    expect(await screen.findByText("Saved as a (2).json")).toBeTruthy();
    s.touch("changed again\n");
    ctrlS();
    await waitFor(() => expect(dialogOpen()).toBe(true));
    await waitFor(() => expect(dialogHas(MESSAGE)).toBe(true));
    expect(dialogHas(MAYBE)).toBe(false);
  });

  it("Close on a tab a script opened asks before it closes, when text is unsaved (MED-3)", async () => {
    const closeSpy = vi.spyOn(window, "close").mockImplementation(() => undefined);
    try {
      server();
      render(`/edit/${NOD}?mode=edit&tab=1`);
      await waitFor(() => expect(content().textContent).toContain("{}"));
      type(0, "x");
      await screen.findByText("Unsaved changes");
      fireEvent.click(screen.getByRole("button", { name: "Close" }));
      await waitFor(() => expect(dialogHas("Leave this file?")).toBe(true));
      expect(closeSpy).not.toHaveBeenCalled();
      fireEvent.click(within(modalWith("Leave this file?")!).getByRole("button", { name: "Leave" }));
      await waitFor(() => expect(closeSpy).toHaveBeenCalled());
      // The page's fallback back, after the refused close, is not asked again: the person chose to leave.
      await new Promise((r) => setTimeout(r, 250));
      expect(dialogHas("Leave this file?")).toBe(false);
    } finally {
      closeSpy.mockRestore();
    }
  });

  it("a save the server answers 401 keeps the draft, names the ended sign-in, and Sign in asks before it leaves (MED-4)", async () => {
    server({ put: () => Response.json({ error: { code: "unauthenticated", details: {} } }, { status: 401 }) });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(SIGNED_OUT)).toBeTruthy();
    expect(content().textContent).toContain("x{}");
    fireEvent.click(within(alert).getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(dialogHas("Leave this file?")).toBe(true));
    expect(router!.state.location.pathname).toBe(`/edit/${NOD}`);
  });

  it("a file whose read is refused because the sign-in has ended says so, with Sign in (MED-4)", async () => {
    server({ read: 401 });
    render();
    expect(await screen.findByText(SIGNED_OUT)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeTruthy();
  });

  it("a copy the server refuses as a bad name says so, with its own words (LOW-2)", async () => {
    const s = server({ post: () => Response.json({ error: { code: "invalid_path", details: {} } }, { status: 400 }) });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText(BAD_NAME)).toBeTruthy();
    expect(screen.queryByText(FAILED)).toBeNull();
  });

  it("a copy whose folder is gone says so, with its own words (LOW-2)", async () => {
    const s = server({ post: () => Response.json({ error: { code: "not_found", details: {} } }, { status: 404 }) });
    const d = await openConflict(s);
    fireEvent.click(within(d).getByRole("button", { name: COPY }));
    expect(await screen.findByText(FOLDER_GONE)).toBeTruthy();
  });

  it("a save the server answers 507 says the storage is full, and offers no retry (LOW-3)", async () => {
    server({ put: () => Response.json({ error: { code: "quota_exceeded", details: {} } }, { status: 507 }) });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(STORAGE_FULL)).toBeTruthy();
    expect(within(alert).queryByRole("button", { name: "Try again" })).toBeNull();
  });

  it("the app mark leaves through the router, so the unsaved guard asks first (LOW-4)", async () => {
    server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    await screen.findByText("Unsaved changes");
    fireEvent.click(document.querySelector<HTMLAnchorElement>(`[data-testid="code-editor-page"] a[href="/files/${FLD}"]`)!);
    await waitFor(() => expect(dialogHas("Leave this file?")).toBe(true));
    expect(router!.state.location.pathname).toBe(`/edit/${NOD}`);
  });

  it("a reload that finishes after the page moved to another file does not put the old file on the page (LOW-5)", async () => {
    let release: () => void = () => undefined;
    const hold = { on: false, gate: new Promise<void>((r) => (release = r)) };
    const s = server({ hold });
    // b.json has its own text, so the page shows which file it holds.
    s.files[NOD_B]!.body = new TextEncoder().encode("bee");
    const d = await openConflict(s);
    hold.on = true;
    fireEvent.click(within(d).getByRole("button", { name: RELOAD }));
    const pop = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-popover:not(.ant-popover-hidden)");
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(within(pop).getByRole("button", { name: "Reload" }));
    // The reload waits for the server. The person leaves for another file, and chooses Leave in the guard.
    await goTo(`/edit/${NOD_B}?mode=edit`);
    await waitFor(() => expect(dialogHas("Leave this file?")).toBe(true));
    fireEvent.click(within(modalWith("Leave this file?")!).getByRole("button", { name: "Leave" }));
    await waitFor(() => expect(content().textContent).toContain("bee"));
    // The old file's reload finishes now: it must not put a.json's text on b.json's page.
    release();
    await pause();
    await pause();
    expect(content().textContent).toContain("bee");
    expect(content().textContent).not.toContain("{}");
  });

  it("a JSON problem is reported in the editor's words, not the parser's (LOW-7)", async () => {
    const LINT_INVALID = "Not valid JSON here.";
    server({ body: new TextEncoder().encode("{") });
    render();
    await screen.findByText("1 problem");
    const messages: string[] = [];
    forEachDiagnostic(view().state, (d) => messages.push(d.message));
    expect(messages).toEqual([LINT_INVALID]);
  });

  it("a .jsonc file is not linted: no marker and no count", async () => {
    server({ name: "a.jsonc", body: new TextEncoder().encode('{ // comment\n  "a": 1\n}\n') });
    render();
    await waitFor(() => expect(content().textContent).toContain('"a"'));
    // The lint package loads only for a JSON file; a short wait gives a late load its chance to draw a marker.
    await new Promise((r) => setTimeout(r, 300));
    expect(document.querySelector(".cm-lint-marker")).toBeNull();
    expect(screen.queryByTestId("lint-count")).toBeNull();
  });
});
