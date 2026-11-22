// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { createAppPassword, listAppPasswords, revokeAppPassword } from "./appPasswords";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const path = "/api/v1/me/app-passwords";

beforeEach(() => session.set(signedAs("olivia")));
afterEach(() => session.set(null));

describe("the app password calls", () => {
  it("list reads the items and whether the mount is on", async () => {
    const item = { id: "apw_1", name: "laptop", access: "read", prefix: "scp_apw_1_Qx7vT2", created_at: "2026-11-10T08:00:00Z", last_used_at: null };
    const { calls } = stubFetch((c: Call) => {
      if (c.url === path && c.method === "GET") return { status: 200, body: { items: [item], mount_enabled: true } };
      return fail(404, "not_found");
    });
    const got = await listAppPasswords();
    expect(got.items).toEqual([item]);
    expect(got.mount_enabled).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("GET");
  });

  it("create sends the name alone: the mount is read-only, the server's default is read", async () => {
    const item = { id: "apw_1", name: "laptop", access: "read", prefix: "scp_apw_1_Qx7vT2", created_at: "2026-11-10T08:00:00Z", last_used_at: null };
    const { calls } = stubFetch((c: Call) => {
      if (c.url === path && c.method === "POST") return { status: 201, body: { app_password: item, secret: "scp_apw_1_tail" } };
      return fail(404, "not_found");
    });
    const got = await createAppPassword("laptop");
    expect(got.app_password).toEqual(item);
    expect(got.secret).toBe("scp_apw_1_tail");
    expect(calls[0].body).toEqual({ name: "laptop" });
  });

  it("revoke deletes the row by id", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === `${path}/apw_1` && c.method === "DELETE") return { status: 204 };
      return fail(404, "not_found");
    });
    await expect(revokeAppPassword("apw_1")).resolves.toBeUndefined();
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("DELETE");
    expect(calls[0].url).toBe(`${path}/apw_1`);
  });
});
