// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import { get } from '../store';
import {
  canRequestFiles,
  cleanLine,
  dropCreateBody,
  dropDateLimits,
  dropDefaultDate,
  dropMetadata,
  dropPatchBody,
  dropProblem,
  dropFormOf,
  dropState,
  dropTitle,
  tooLong,
  uploaderProblem,
  type DropForm,
} from './drop';
import { encodeMetadata } from '../files/tus';
import { dateInputValue } from './limits';
import type { ShareLink } from './links';

const DAY = 24 * 60 * 60 * 1000;

// decodeMetadataForTest reads one Upload-Metadata value: base64 of UTF-8.
function decodeMetadataForTest(b64: string): string {
  return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
}
const now = new Date(2026, 9, 29, 10, 0, 0); // local time, as the date inputs are

function form(over: Partial<DropForm> = {}): DropForm {
  return {
    date: dateInputValue(new Date(now.getTime() + 10 * DAY)),
    password: '',
    passwordChanged: false,
    title: 'Send me the invoices',
    message: 'Please name each file with the month.',
    askName: true,
    notify: true,
    ...over,
  };
}

describe('who sees Request files', () => {
  it('shows it only to a manager on an account that allows public links', () => {
    expect(canRequestFiles(true, 'manager')).toBe(true);
  });

  it('hides it from an editor, even where editors may share', () => {
    expect(canRequestFiles(true, 'editor')).toBe(false);
    expect(canRequestFiles(true, 'contributor')).toBe(false);
  });

  it('hides it when the account has public links off, or the caller has no share right', () => {
    expect(canRequestFiles(false, 'manager')).toBe(false);
    expect(canRequestFiles(true, null)).toBe(false);
  });
});

describe('the end date of a drop link', () => {
  it('defaults to 30 days, and to the sharing cap when that is shorter', () => {
    expect(dropDefaultDate(now, 0)).toBe(dateInputValue(new Date(now.getTime() + 30 * DAY)));
    expect(dropDefaultDate(now, 7)).toBe(dateInputValue(new Date(now.getTime() + 7 * DAY)));
  });

  it('offers tomorrow as the earliest date and the cap as the latest', () => {
    const lim = dropDateLimits(now, 0);
    expect(lim.earliest).toBe(dateInputValue(new Date(now.getTime() + DAY)));
    expect(lim.latest).toBe(dateInputValue(new Date(now.getTime() + 365 * DAY)));
    expect(dropDateLimits(now, 90).latest).toBe(dateInputValue(new Date(now.getTime() + 90 * DAY)));
  });

  it('refuses an empty end date: a drop link must end', () => {
    expect(dropProblem(form({ date: '' }), now, 0, 8)).toBe('date');
    expect(dropCreateBody(form({ date: '' }), now, 0, 8)).toBeNull();
  });

  it('pulls a date past the cap back to the cap in the body, as anyone links do', () => {
    const body = dropCreateBody(form({ date: '2031-01-01' }), now, 0, 8);
    expect(body).not.toBeNull();
    const end = Date.parse(body?.expires_at as string);
    expect(end - now.getTime()).toBeLessThanOrEqual(365 * DAY + DAY);
  });
});

describe('the body of a drop link', () => {
  it('makes an anyone link that only receives files, with the switches as chosen', () => {
    const body = dropCreateBody(form({ askName: false, notify: false }), now, 0, 8);
    expect(body).toMatchObject({ audience: 'anyone', upload_only: true, ask_name: false, notify: false });
    expect(body).not.toHaveProperty('role');
    expect(body).not.toHaveProperty('password');
  });

  it('sends a password only when one is typed', () => {
    expect(dropCreateBody(form({ password: 'correct horse' }), now, 0, 8)).toHaveProperty('password', 'correct horse');
    expect(dropCreateBody(form({ password: '' }), now, 0, 8)).not.toHaveProperty('password');
  });

  it('refuses a short password, the same rule an anyone link has', () => {
    expect(dropProblem(form({ password: 'short', passwordChanged: true }), now, 0, 8)).toBe('password_short');
    expect(dropProblem(form({ password: 'short', passwordChanged: false }), now, 0, 8)).toBeNull();
  });

  it('removes line breaks from the title, and refuses a title over 100 characters', () => {
    const body = dropCreateBody(form({ title: 'Invoices\nfor October' }), now, 0, 8);
    expect(body?.title).toBe('Invoices for October');
    expect(dropProblem(form({ title: 'x'.repeat(101) }), now, 0, 8)).toBe('title_long');
    expect(dropProblem(form({ title: 'x'.repeat(100) }), now, 0, 8)).toBeNull();
  });

  it('refuses a message over 1000 characters', () => {
    expect(dropProblem(form({ message: 'y'.repeat(1001) }), now, 0, 8)).toBe('message_long');
    expect(dropProblem(form({ message: 'y'.repeat(1000) }), now, 0, 8)).toBeNull();
  });

  it('sends a password change on save, and an empty one to remove it', () => {
    expect(dropPatchBody(form({ passwordChanged: true, password: '' }), now, 0, 8)).toHaveProperty('password', '');
    expect(dropPatchBody(form({ passwordChanged: false }), now, 0, 8)).not.toHaveProperty('password');
    expect(dropPatchBody(form({ date: '' }), now, 0, 8)).toBeNull();
  });
});

describe('the uploader fields', () => {
  it('requires a name and an email when the link asks for them', () => {
    expect(uploaderProblem(true, { name: '', email: 'a@b.test' })).toBe('name_required');
    expect(uploaderProblem(true, { name: 'Ann', email: '' })).toBe('email_required');
    expect(uploaderProblem(true, { name: 'Ann', email: 'ann@example.test' })).toBeNull();
  });

  it('leaves both optional otherwise, but still checks what is typed', () => {
    expect(uploaderProblem(false, { name: '', email: '' })).toBeNull();
    expect(uploaderProblem(false, { name: '', email: 'not an address' })).toBe('email_invalid');
  });

  it('caps the name at 100 and the email at 254 characters', () => {
    expect(uploaderProblem(false, { name: 'n'.repeat(101), email: '' })).toBe('name_long');
    expect(uploaderProblem(false, { name: 'n'.repeat(100), email: '' })).toBeNull();
    const longEmail = `${'a'.repeat(250)}@b.test`;
    expect(longEmail.length).toBeGreaterThan(254);
    expect(uploaderProblem(false, { name: '', email: longEmail })).toBe('email_long');
  });

  it('counts characters, not bytes', () => {
    expect(tooLong('שלום'.repeat(25), 100)).toBe(false);
    expect(tooLong('שלום'.repeat(26), 100)).toBe(true);
  });

  it('cleans line breaks out of what the visitor types', () => {
    expect(cleanLine(' Ann\r\nBob ')).toBe('Ann Bob');
  });
});

describe('the metadata of a file sent through a drop link', () => {
  it('carries the file name and the uploader fields, and no folder, parent, path or conflict setting', () => {
    const meta = dropMetadata('report.pdf', { name: 'Ann', email: 'ann@example.test' });
    expect(Object.keys(meta).sort()).toEqual(['name', 'uploader_email', 'uploader_name']);
    expect(meta.name).toBe('report.pdf');
  });

  it('encodes each value as base64 UTF-8 in the header, and leaves out an empty uploader field', () => {
    const header = encodeMetadata(dropMetadata('שם.txt', { name: 'שרה', email: '' }));
    expect(header).not.toContain('uploader_email');
    const parts = Object.fromEntries(header.split(',').map((p) => p.split(' ') as [string, string]));
    expect(decodeMetadataForTest(parts.name)).toBe('שם.txt');
    expect(decodeMetadataForTest(parts.uploader_name)).toBe('שרה');
  });

  it('never sends a line break in an uploader field', () => {
    const meta = dropMetadata('a.txt', { name: 'Ann\nBob', email: '' });
    expect(meta.uploader_name).toBe('Ann Bob');
  });
});

describe('the drop page title', () => {
  it('shows the link title, or the fallback when it has none', () => {
    expect(dropTitle({ title: 'Invoices' }, 'Send files')).toBe('Invoices');
    expect(dropTitle({ title: '  ' }, 'Send files')).toBe('Send files');
  });
});

describe('the drop link the dialog shows', () => {
  const link = (over: Partial<ShareLink> = {}): ShareLink => ({
    id: 'shl_drop0000000000000000000000',
    node_id: 'nod_dir00000000000000000000000',
    audience: 'anyone',
    role: '',
    expires_at: '2026-11-28T09:00:00.000Z',
    created_at: '2026-10-29T09:00:00.000Z',
    inherited_from: null,
    upload_only: true,
    title: 'Invoices',
    message: 'One file per month.',
    ask_name: true,
    notify: false,
    password_set: true,
    ...over,
  });

  it('shows off with no link, on with a live one, and paused when policy pauses it', () => {
    expect(dropState(null)).toBe('off');
    expect(dropState(link())).toBe('on');
    expect(dropState(link({ paused: true }))).toBe('paused');
  });

  it('reads the form from the link, and never reads the password back', () => {
    const f = dropFormOf(link(), (iso) => iso.slice(0, 10));
    expect(f).toMatchObject({ date: '2026-11-28', title: 'Invoices', message: 'One file per month.', askName: true, notify: false });
    expect(f.password).toBe('');
    expect(f.passwordChanged).toBe(false);
  });
});

describe("a drop page's upload manager", () => {
  it('lists only the files it was given, through its own sink, and leaves the tray store alone', async () => {
    vi.stubGlobal('window', {
      location: { pathname: '/s', search: '', hash: '' },
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    });
    try {
      const U = await import('../files/uploads');
      let seen: string[] = [];
      const m = new U.UploadManager(() => 0, {
        bearer: () => null,
        metadata: (it) => dropMetadata(it.name, { name: '', email: '' }),
        fresh: async () => undefined,
        sink: (items) => {
          seen = items.map((i) => i.name);
        },
      });
      m.add([{ file: new File(['x'], 'one.txt'), target: { folder: '' } }]);
      expect(seen).toEqual(['one.txt']);
      expect(get(U.uploads)).toEqual([]);
      m.halt();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
