// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../session";
import type { Node } from "../files/types";
import { copyName, headEtag, keepCopy } from "./conflict";

// ROOT is the folder's root node: a directory with no parent. A file in the root names it as its parent_id.
const ROOT = { id: "nod_root", folder_id: "fld_b", parent_id: null, name: "", kind: "dir", etag: '"root"' };

// file is the open file, in the root of its folder.
const file = { id: "nod_a1", folder_id: "fld_b", parent_id: "nod_root", name: "a.json", kind: "file" } as unknown as Node;

// fake stubs fetch: a GET answers with the root node (the ancestor walk), and every other request goes to handler.
function fake(handler: (url: string, init: RequestInit) => Response) {
  const f = vi.fn(async (url: string, init: RequestInit = {}) => {
    if ((init.method ?? "GET") === "GET") return Response.json(ROOT);
    return handler(url, init);
  });
  vi.stubGlobal("fetch", f);
  return f;
}

const taken = () => Response.json({ error: { code: "precondition_failed", details: {} } }, { status: 412 });
const created = (name: string) =>
  Response.json({ id: "nod_copy", folder_id: "fld_b", parent_id: "nod_root", name, kind: "file", etag: '"c"' }, { status: 201 });

beforeEach(() => {
  session.set({ user: { id: "usr_1", role: "user" }, csrf: "csrf_test" } as never);
});

afterEach(() => {
  vi.unstubAllGlobals();
  session.set(null);
});

describe("copyName", () => {
  it.each([
    ["a.json", 2, "a (2).json"],
    ["archive.tar.gz", 3, "archive.tar (3).gz"],
    ["Makefile", 2, "Makefile (2)"],
    [".env", 2, ".env (2)"],
  ])("%s at n=%i is %s", (name, n, want) => {
    expect(copyName(name, n)).toBe(want);
  });
});

describe("headEtag", () => {
  it("reads the current ETag with HEAD, and makes no GET", async () => {
    const f = fake(() => new Response(null, { status: 200, headers: { ETag: '"e1"' } }));
    expect(await headEtag("nod_a1")).toEqual({ ok: true, etag: '"e1"' });
    expect(f).toHaveBeenCalledTimes(1);
    expect(f.mock.calls[0]![1]).toMatchObject({ method: "HEAD" });
  });

  it("maps a refusal to the failure it means", async () => {
    fake(() => new Response(null, { status: 403 }));
    expect(await headEtag("nod_a1")).toEqual({ ok: false, why: "forbidden" });
  });
});

describe("keepCopy", () => {
  it("tries the next free name, each try sent with If-None-Match *", async () => {
    const f = fake((url) => (url.includes("(2)") ? taken() : created("a (3).json")));
    const got = await keepCopy(file, "application/json", new TextEncoder().encode("x{}"));
    expect(got.ok).toBe(true);
    const posts = f.mock.calls.filter((c) => c[1]?.method === "POST");
    expect(posts).toHaveLength(2);
    expect(posts[1]![1]?.headers).toMatchObject({ "If-None-Match": "*", "Content-Type": "application/json" });
    expect(new URL(posts[1]![0] as string, "http://localhost").searchParams.get("path")).toBe("/a (3).json");
  });

  it("stops at twenty tries and reports that no name is free", async () => {
    const f = fake(() => taken());
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "noName", sent: true });
    expect(f.mock.calls.filter((c) => c[1]?.method === "POST")).toHaveLength(20);
  });

  it("stops at any other answer and reports it as a failure", async () => {
    const f = fake(() => new Response(null, { status: 403 }));
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "forbidden", sent: true });
    expect(f.mock.calls.filter((c) => c[1]?.method === "POST")).toHaveLength(1);
  });

  it("says whether a request was sent: a folder walk that fails sent none, a lost copy was sent (LOW-B)", async () => {
    // The walk is a GET of the folder; the copy is a POST. A walk that fails before any POST has no copy in flight.
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit = {}) => {
      if ((init.method ?? "GET") === "GET") throw new TypeError("offline");
      return created("x");
    }));
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "failed", sent: false });
    fake(() => {
      throw new TypeError("connection lost");
    });
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "failed", sent: true });
  });

  it("names a copy in a folder two levels down, outermost folder first (LOW-1)", async () => {
    const nodes: Record<string, unknown> = {
      nod_dir: { id: "nod_dir", folder_id: "fld_b", parent_id: "nod_root", name: "docs", kind: "dir", etag: '"d"' },
      nod_sub: { id: "nod_sub", folder_id: "fld_b", parent_id: "nod_dir", name: "inner", kind: "dir", etag: '"s"' },
    };
    const f = vi.fn(async (url: string, init: RequestInit = {}) => {
      if ((init.method ?? "GET") === "GET") {
        const id = new URL(url, "http://localhost").pathname.split("/").pop() ?? "";
        return Response.json(nodes[id] ?? ROOT);
      }
      return created("a (2).json");
    });
    vi.stubGlobal("fetch", f);
    const deep = { ...file, parent_id: "nod_sub" } as unknown as Node;
    const got = await keepCopy(deep, "application/json", new TextEncoder().encode("x{}"));
    expect(got.ok).toBe(true);
    const post = f.mock.calls.find((c) => c[1]?.method === "POST");
    expect(new URL(post![0] as string, "http://localhost").searchParams.get("path")).toBe("/docs/inner/a (2).json");
  });

  it("names a copy the server refuses as a bad name, and one whose folder is gone, with their own reasons (LOW-2)", async () => {
    fake(() => Response.json({ error: { code: "invalid_path", details: {} } }, { status: 400 }));
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "badName", sent: true });
    fake(() => Response.json({ error: { code: "not_found", details: {} } }, { status: 404 }));
    expect(await keepCopy(file, "application/json", new Uint8Array())).toEqual({ ok: false, why: "folderGone", sent: true });
  });
});
