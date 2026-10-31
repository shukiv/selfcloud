// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { api, ApiError } from "./api";
import { endImpersonation } from "./impersonation";
import { errorMessage } from "./errors";
import { flash, session, type Session } from "./session";
import { get } from "./store";
import { fail, stubFetch, type Call } from "../test/fetchStub";

const member = { id: "usr_m", email: "mateo@example.com", display_name: "Mateo Member", role: "member", locale: "en", status: "active" };
const admin = { id: "adm_1", email: "ada@example.com", display_name: "Ada Admin", role: "admin", locale: "en", status: "active" };

// impersonatingMember is the browser as a tenant user, sign-in as started by the admin.
function impersonatingMember(): Session {
  return {
    user: member,
    account: { id: "acc_1", name: "Acme" },
    csrf: "imp-csrf",
    publicLinks: false,
    impersonation: { endsAt: "2026-10-31T13:00:00Z" },
  } as unknown as Session;
}

const adminMe = { user: admin, account: null, csrf_token: "admin-csrf", tenant_access: true, mail_configured: false };

beforeEach(() => {
  window.history.replaceState(null, "", "/files");
  flash.set(null);
});
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

describe("an expired sign-in as", () => {
  it("sends one end call however many requests find it ended, and restores the admin session", async () => {
    session.set(impersonatingMember());
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/files") return fail(401, "unauthenticated", { reason: "impersonation_ended" });
      if (c.url === "/api/v1/auth/impersonation/end") return { status: 200, body: { user: admin, csrf_token: "admin-csrf" } };
      if (c.url === "/api/v1/me") return { status: 200, body: adminMe };
      return { status: 500, body: { error: { code: "internal" } } };
    });

    const answers = await Promise.allSettled([api("GET", "/api/v1/files"), api("GET", "/api/v1/files"), api("GET", "/api/v1/files")]);

    expect(answers.every((a) => a.status === "rejected")).toBe(true);
    await new Promise((r) => setTimeout(r, 0));
    expect(calls.filter((c) => c.url === "/api/v1/auth/impersonation/end")).toHaveLength(1);
    const now = get(session);
    expect(now?.user.role).toBe("admin");
    expect(now?.csrf).toBe("admin-csrf");
    expect(now?.impersonation).toBeFalsy();
    expect(get(flash)).toBe("impersonation.expired");
    expect(window.location.pathname).toBe("/admin/tenants");
  });

  it("sends the browser to sign-in when the admin session cannot be restored", async () => {
    session.set(impersonatingMember());
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/impersonation/end") return fail(401, "unauthenticated");
      return { status: 200, body: {} };
    });

    await endImpersonation(false);

    expect(get(session)).toBeNull();
    expect(window.location.pathname).toBe("/login");
  });
});

describe("refusals while signed in as another person", () => {
  it("a refusal that names the impersonation says why, whatever its code", () => {
    const err = new ApiError(403, "forbidden", { reason: "impersonation" }, 0);
    expect(errorMessage(err)).toEqual({ key: "error.impersonation" });
  });

  it("a refused password check of the admin says which password is wrong", () => {
    const err = new ApiError(403, "forbidden", {}, 0);
    expect(errorMessage(err, "admin")).toEqual({ key: "error.adminPassword" });
  });
});
