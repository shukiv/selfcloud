// SPDX-License-Identifier: AGPL-3.0-or-later

import "../test/rangeRects";
import { fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { EditorView } from "@codemirror/view";
import { MemoryRouter, Route, Routes } from "react-router-dom";
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
const ETAG = '"0123abcd"';
const NEW_ETAG = '"feedbeef"';
const MiB = 1024 * 1024;

interface Options {
  role?: string;
  body?: Uint8Array;
  size?: number;
  mime?: string;
  // put answers a save; the default saves and answers with the node's new ETag.
  put?: (init: RequestInit) => Response | Promise<Response>;
}

// server replaces fetch for one test: the node's record, its bytes, and the answers to a save. It records every request.
function server(o: Options = {}) {
  const body = o.body ?? new TextEncoder().encode("{}");
  const mime = o.mime ?? "application/json";
  const calls: Array<{ method: string; url: string; init: RequestInit }> = [];
  let etag = ETAG;
  const f = vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? "GET";
    calls.push({ method, url, init });
    if (method === "GET" && url === `/api/v1/nodes/${NOD}`) {
      return Response.json({
        id: NOD,
        folder_id: "fld_b",
        parent_id: null,
        name: "a.json",
        kind: "file",
        size: o.size ?? body.length,
        mime_type: mime,
        etag,
        created_at: "2026-10-01T00:00:00Z",
        modified_at: "2026-10-01T00:00:00Z",
        role: o.role ?? "editor",
        no_download: false,
        editors_can_share: false,
      });
    }
    if (method === "GET" && url.startsWith(`/api/v1/nodes/${NOD}/content`)) {
      return new Response(body as unknown as BodyInit, { status: 200, headers: { ETag: etag } });
    }
    if (method === "PUT" && url === `/api/v1/nodes/${NOD}/content`) {
      if (o.put) return o.put(init);
      etag = NEW_ETAG;
      return Response.json({ id: NOD, folder_id: "fld_b", parent_id: null, name: "a.json", kind: "file", size: 2, mime_type: mime, etag: NEW_ETAG, created_at: "2026-10-01T00:00:00Z", modified_at: "2026-10-01T00:00:01Z" });
    }
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", f);
  return { calls, puts: () => calls.filter((c) => c.method === "PUT") };
}

function render(path = `/edit/${NOD}?mode=edit`) {
  return rtlRender(
    <MemoryRouter initialEntries={[path]}>
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

beforeEach(() => {
  goMock.mockClear();
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

  it("does not save a file that has not changed", async () => {
    const s = server();
    render();
    await waitFor(() => expect(content().textContent).toContain("{}"));
    ctrlS();
    await new Promise((r) => setTimeout(r, 20));
    expect(s.puts()).toHaveLength(0);
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

  it("a contributor who opens the editor by its address sees View only and no Save", async () => {
    const s = server({ role: "contributor" });
    render();
    expect(await screen.findByText("View only")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    await waitFor(() => expect(content().getAttribute("aria-readonly")).toBe("true"));
    // Ctrl+S is still taken (the browser's save dialog does not open), and nothing is sent.
    expect(ctrlS()).toBe(false);
    await new Promise((r) => setTimeout(r, 20));
    expect(s.puts()).toHaveLength(0);
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
});
