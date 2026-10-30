// SPDX-License-Identifier: AGPL-3.0-or-later

// Fixtures for the installation admin tests. Only the tests import this file.
import type { Session } from "../lib/session";
import type { Account, ApiKey, Info, Person, Usage } from "../lib/manage/types";

export function signedAs(role: "admin" | "owner" | "member", id = `${role}_1`): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role, locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

export function account(over: Partial<Account> = {}): Account {
  return {
    id: "acc_1",
    external_id: null,
    name: "Acme Ltd",
    status: "active",
    quota_bytes: 5_000_000_000,
    used_bytes: 1_000_000_000,
    used_files: 3,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-02T00:00:00Z",
    ...over,
  };
}

export function usage(over: Partial<Usage> = {}): Usage {
  return {
    quota_bytes: 5_000_000_000,
    used_bytes: 1_000_000_000,
    reserved_bytes: 0,
    available_bytes: 4_000_000_000,
    files: 3,
    folders: 1,
    users_active: 2,
    users_pending: 0,
    ...over,
  };
}

export function person(over: Partial<Person> = {}): Person {
  return {
    id: "usr_1",
    external_id: null,
    email: "ana@example.com",
    display_name: "Ana Admin",
    locale: "en",
    role: "admin",
    status: "active",
    verified_at: "2026-10-01T00:00:00Z",
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...over,
  };
}

export function key(over: Partial<ApiKey> = {}): ApiKey {
  return {
    id: "key_1",
    name: "CI",
    prefix: "scl_abc",
    created_at: "2026-10-01T00:00:00Z",
    expires_at: null,
    revoked_at: null,
    last_used_at: null,
    ...over,
  };
}

export const info: Info = {
  name: "Selfcloud",
  version: "1.4.2",
  public_url: "https://drive.example.com",
  web_interface: true,
  limits: { max_file_size: 100_000_000_000, max_chunk_size: 100_000_000 },
};

// The secret every fixture link carries. A test looks for it to prove it is shown once and kept nowhere else.
export const LINK = "https://drive.example.com/verify#token=SECRET-ONE-TIME-VALUE";
