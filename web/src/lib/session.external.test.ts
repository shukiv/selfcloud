// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { get } from "./store";
import { session, setMe, setSignedIn } from "./session";
import type { Me, SignedIn } from "./types";

const user = { id: "usr_ann", email: "ann@example.com", display_name: "Ann", locale: "en", role: "member", status: "active" } as Me["user"];

function me(extra: Partial<Me> = {}): Me {
  return {
    user,
    account: { id: "acc_1", name: "Acme" },
    csrf_token: "csrf-ann",
    personal_folder_id: null,
    public_links: true,
    ...extra,
  };
}

afterEach(() => session.set(null));

describe("the session reads the email proof and the external sharing switch", () => {
  it("setMe keeps both flags the server gives", () => {
    setMe(me({ email_proved: true, account_external_sharing: true }));
    expect(get(session)?.emailProved).toBe(true);
    expect(get(session)?.accountExternalSharing).toBe(true);
  });

  it("an older server that gives neither leaves both off", () => {
    setMe(me());
    expect(get(session)?.emailProved).toBe(false);
    expect(get(session)?.accountExternalSharing).toBe(false);
  });

  it("a fresh sign-in starts with both off until the account is read", () => {
    const signed: SignedIn = { user, csrf_token: "csrf-ann" };
    setSignedIn(signed);
    expect(get(session)?.emailProved ?? false).toBe(false);
    expect(get(session)?.accountExternalSharing ?? false).toBe(false);
  });
});