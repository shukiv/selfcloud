// SPDX-License-Identifier: AGPL-3.0-or-later

import "../test/rangeRects";
import { act, fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { EditorView } from "@codemirror/view";
import { MemoryRouter, Route, Routes, useNavigate, type NavigateFunction } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const goMock = vi.hoisted(() => vi.fn());
vi.mock("../lib/router", () => ({ go: goMock }));

import CodeEditPage from "./CodeEditPage";
import { session } from "../lib/session";

// The page's meta nonce, as the server writes it: CodeMirror's style sheet is written with it.
const meta = document.createElement("meta");
meta.setAttribute("property", "csp-nonce");
meta.setAttribute("nonce", "nonce-for-tests");
document.head.appendChild(meta);

const NOD = "nod_a1";
const NOD_B = "nod_b2";
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
  noDownload?: boolean;
  // put answers a save of the first file, instead of the server's own rule (the If-Match check runs first).
  put?: (init: RequestInit) => Response | Promise<Response>;
}

interface Stored {
  id: string;
  name: string;
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
  const files: Record<string, Stored> = {
    [NOD]: {
      id: NOD,
      name: "a.json",
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
  const nodeJSON = (f: Stored) => ({
    id: f.id,
    folder_id: "fld_b",
    parent_id: null,
    name: f.name,
    kind: "file",
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
    const m = /^\/api\/v1\/nodes\/([^/?]+)(\/content)?(\?.*)?$/.exec(url);
    const file = m ? files[m[1]!] : undefined;
    if (m === null || file === undefined) return new Response(null, { status: 404 });
    if (method === "GET" && m[2] === undefined) return Response.json(nodeJSON(file));
    if (method === "GET") {
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
  return { calls, answers, puts: () => calls.filter((c) => c.method === "PUT") };
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

function render(path = `/edit/${NOD}?mode=edit`) {
  return rtlRender(
    <MemoryRouter initialEntries={[path]}>
      <Grab />
      <Routes>
        <Route path="/edit/:nod" element={<CodeEditPage />} />
      </Routes>
    </MemoryRouter>,
  );
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
  navigateTo = null;
  // A signed-in browser session: the reads and the save are made under it.
  session.set({ user: { id: "usr_1", role: "user" }, csrf: "csrf_test" } as never);
});

afterEach(() => {
  vi.unstubAllGlobals();
  session.set(null);
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

  it("shows a conflict with a reload choice, and keeps the draft until the person chooses to reload", async () => {
    const s = server({ put: () => Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 }) });
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    type(0, "x");
    ctrlS();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("This file changed since you opened it. Reload it to see the saved version.")).toBeTruthy();
    expect(content().textContent).toContain("x{}");
    fireEvent.click(within(alert).getByRole("button", { name: "Reload" }));
    // Reloading throws the draft away, so it asks first: the confirmation's own Reload button does it.
    const pop = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-popover:not(.ant-popover-hidden)");
      expect(el).not.toBeNull();
      return el!;
    });
    fireEvent.click(within(pop).getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(content().textContent).toBe("{}"));
    expect(s.puts()).toHaveLength(1);
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
    act(() => navigateTo!(`/edit/${NOD_B}?mode=edit`));
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
