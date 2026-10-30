// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { afterSignIn, safeNext } from './next';

describe('safeNext', () => {
  it('keeps a same-site path, with its query and fragment', () => {
    expect(safeNext('/files')).toBe('/files');
    expect(safeNext('/files/a%20b?x=1#top')).toBe('/files/a%20b?x=1#top');
    expect(safeNext('/shared')).toBe('/shared');
  });

  it('sends anything else home', () => {
    const refused = [
      null,
      undefined,
      '',
      'files',
      '//evil.example',
      '//evil.example/files',
      '///evil.example',
      '/\\evil.example',
      '/\\evil.example/files',
      '/\t/files',
      '\\\\evil.example',
      '/\t/evil.example',
      '/\n/evil.example',
      ' /files',
      '/files\u0000',
      'https://evil.example/',
      'http://evil.example',
      'javascript:alert(1)',
      'data:text/html,hi',
      'mailto:a@b.example',
      '/' + 'a'.repeat(2048),
      '/.//evil.example',
      '/a/..//evil.example',
      '/x/./..//evil.example',
    ];
    for (const raw of refused) {
      expect(safeNext(raw as string | null | undefined), String(raw)).toBe('/');
    }
  });
});

describe('afterSignIn', () => {
  it('goes where safeNext says, except back to a sign-in page', () => {
    expect(afterSignIn('/files?x=1')).toBe('/files?x=1');
    expect(afterSignIn('/login')).toBe('/');
    expect(afterSignIn('/login?next=/files')).toBe('/');
    expect(afterSignIn('/login/')).toBe('/');
    expect(afterSignIn('/sign-in?x=1')).toBe('/sign-in?x=1');
    expect(afterSignIn(null)).toBe('/');
  });
});

// A sign-in goes straight to the signed-in person's own home. Going to "/" first and letting the gate
// send an admin on again is a second navigation that can be lost, which left a blank page after an
// admin's sign-in (v4a).
describe("afterSignIn goes straight to the home of the person's role", () => {
  const admin = { id: "usr_a", role: "admin" } as unknown as import("./types").User;
  const member = { id: "usr_m", role: "member" } as unknown as import("./types").User;

  it("an admin with no next goes to the tenants list, in one step", () => {
    expect(afterSignIn(null, admin)).toBe("/admin/tenants");
    expect(afterSignIn("/", admin)).toBe("/admin/tenants");
  });

  it("an admin with a next address goes there", () => {
    expect(afterSignIn("/admin/installation", admin)).toBe("/admin/installation");
  });

  it("a member with no next goes home, and a path back to the sign-in page still goes home", () => {
    expect(afterSignIn(null, member)).toBe("/");
    expect(afterSignIn("/login", member)).toBe("/");
  });
});

describe("afterSignIn only treats the sign-in page itself as the sign-in page (guard: exact /login match)", () => {
  it("a path that only starts with the sign-in word is an ordinary path", () => {
    const someone = { id: "usr_m", role: "member" } as unknown as import("./types").User;
    expect(afterSignIn("/login-help", someone)).toBe("/login-help");
  });
});
