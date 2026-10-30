// SPDX-License-Identifier: AGPL-3.0-or-later

import { vi } from "vitest";

// One request the page made: what it asked, where, with which bearer and body. The page tests read these to show what
// reached the server, and that a secret never travelled in an address.
export interface Call {
  method: string;
  url: string;
  auth: string | null;
  body: unknown;
}

// An answer is a status and, optionally, a JSON body. An error answer uses the server's envelope.
export interface Answer {
  status: number;
  body?: unknown;
}

export function fail(status: number, code: string, details: Record<string, unknown> = {}): Answer {
  return { status, body: { error: { code, details } } };
}

// stubFetch replaces fetch for one test. The answer function sees each call and returns what the server would say.
export function stubFetch(answer: (c: Call) => Answer | Promise<Answer>) {
  const calls: Call[] = [];
  const fn = vi.fn(async (url: string, init: RequestInit = {}) => {
    const headers = (init.headers ?? {}) as Record<string, string>;
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as unknown) : null;
    const call: Call = { method: init.method ?? "GET", url, auth: headers.Authorization ?? null, body };
    calls.push(call);
    const a = await answer(call);
    return new Response(a.body === undefined ? null : JSON.stringify(a.body), {
      status: a.status,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fn);
  return { calls };
}
