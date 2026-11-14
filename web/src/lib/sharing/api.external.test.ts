// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../session";
import { get } from "../store";
import { ApiError } from "../apiError";
import { fail, stubFetch } from "../../test/fetchStub";
import {
  addExternalPermission,
  confirmEmailProof,
  listExternalPermissions,
  proveEmail,
  removeExternalPermission,
  setSharingPolicy,
  sharedExternal,
} from "./api";

function signedIn(): Session {
  return {
    user: { id: "usr_ann", email: "ann@example.com", display_name: "Ann", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-ann",
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedIn()));
afterEach(() => session.set(null));

describe("the sharing policy patch", () => {
  it("sends only the switch it is given, and returns the whole policy", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: { public_links: true, anyone_links: 2, external_sharing: true } }));
    const got = await setSharingPolicy("acc_1", { external_sharing: true });
    expect(calls).toHaveLength(1);
    expect(calls[0].method).toBe("PATCH");
    expect(calls[0].url).toBe("/api/v1/accounts/acc_1/sharing-policy");
    expect(calls[0].body).toEqual({ external_sharing: true });
    expect(got.external_sharing).toBe(true);
    expect(got.public_links).toBe(true);
  });

  it("a public-links change still names only public_links", async () => {
    const { calls } = stubFetch(() => ({ status: 200, body: { public_links: false, anyone_links: 0 } }));
    await setSharingPolicy("acc_1", { public_links: false });
    expect(calls[0].body).toEqual({ public_links: false });
  });
});

describe("the external permissions of a node", () => {
  it("lists them from the node's external-permissions route", async () => {
    const grant = { email: "mia@else.example", role: "viewer", created_at: "2026-11-01T00:00:00Z", expires_at: null, created_by_name: "Ann" };
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [grant] } }));
    const got = await listExternalPermissions("nod_board01");
    expect(calls[0].method).toBe("GET");
    expect(calls[0].url).toBe("/api/v1/nodes/nod_board01/external-permissions");
    expect(got.items).toEqual([grant]);
  });

  it("adds one with the address, the viewer role and an end date when given", async () => {
    const { calls } = stubFetch(() => ({
      status: 200,
      body: { email: "mia@else.example", role: "viewer", created_at: "2026-11-01T00:00:00Z", expires_at: "2026-12-01T00:00:00Z", created_by_name: "Ann" },
    }));
    await addExternalPermission("nod_board01", { email: "mia@else.example", role: "viewer", expires_at: "2026-12-01T00:00:00Z" });
    expect(calls[0].method).toBe("POST");
    expect(calls[0].url).toBe("/api/v1/nodes/nod_board01/external-permissions");
    expect(calls[0].body).toEqual({ email: "mia@else.example", role: "viewer", expires_at: "2026-12-01T00:00:00Z" });
  });

  it("removes one at the encoded address, so an address with a plus never breaks the route", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    await removeExternalPermission("nod_board01", "mia+tag@else.example");
    expect(calls[0].method).toBe("DELETE");
    expect(calls[0].url).toBe(`/api/v1/nodes/nod_board01/external-permissions/${encodeURIComponent("mia+tag@else.example")}`);
  });
});

describe("the items shared from other accounts", () => {
  it("reads the shared-external route with its proved flag", async () => {
    const { calls } = stubFetch(() => ({
      status: 200,
      body: {
        items: [
          {
            id: "nod_ext0001",
            name: "Report.pdf",
            kind: "file",
            size: 10,
            modified_at: "2026-11-02T00:00:00Z",
            mime: "application/pdf",
            account_name: "Globex",
            shared_by_name: "Mia Outside",
            role: "viewer",
            expires_at: null,
          },
        ],
        email_proved: true,
      },
    }));
    const got = await sharedExternal();
    expect(calls[0].method).toBe("GET");
    expect(calls[0].url).toBe("/api/v1/shared/external");
    expect(got.email_proved).toBe(true);
    expect(got.items[0].account_name).toBe("Globex");
  });
});

describe("the email proof", () => {
  it("asks the server to send the proof mail", async () => {
    const { calls } = stubFetch(() => ({ status: 202, body: { proved: false, sent: true } }));
    const got = await proveEmail();
    expect(calls[0].method).toBe("POST");
    expect(calls[0].url).toBe("/api/v1/me/email-proof");
    expect(got).toEqual({ proved: false, sent: true });
  });

  it("redeems the mailed token at the auth route", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    await confirmEmailProof("tok_proof");
    expect(calls[0].method).toBe("POST");
    expect(calls[0].url).toBe("/api/v1/auth/email-proof");
    expect(calls[0].body).toEqual({ token: "tok_proof" });
  });

  it("a bad token is an answer the page reads, not a redirect to sign-in", async () => {
    stubFetch(() => fail(400, "invalid_request"));
    const before = window.location.pathname;
    await expect(confirmEmailProof("tok_bad")).rejects.toBeInstanceOf(ApiError);
    expect(window.location.pathname).toBe(before);
    expect(get(session)).not.toBeNull();
  });
});