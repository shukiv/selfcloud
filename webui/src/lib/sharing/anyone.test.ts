// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from '../apiError';
import {
  ANYONE_MAX_DAYS,
  capDays,
  crumbsTo,
  defaultAnyoneEnd,
  anyoneExpiresAt,
  latestAnyoneEnd,
  outcomeOf,
  passwordProblem,
  previewKind,
  renewDelay,
  visitStep,
  type NodeRef,
} from './anyone';

const DAY = 24 * 60 * 60 * 1000;
const now = new Date(2026, 9, 29, 10, 0, 0); // local time, so the date input maps to the same day

describe('expiry of an anyone link', () => {
  it('defaults to 30 days, and to the sharing cap when that is shorter', () => {
    expect(defaultAnyoneEnd(now, 0).getTime() - now.getTime()).toBe(30 * DAY);
    expect(defaultAnyoneEnd(now, 7).getTime() - now.getTime()).toBe(7 * DAY);
    expect(defaultAnyoneEnd(now, 900).getTime() - now.getTime()).toBe(30 * DAY);
  });

  it('caps at 365 days, and at a smaller sharing cap', () => {
    expect(capDays(0)).toBe(ANYONE_MAX_DAYS);
    expect(capDays(900)).toBe(ANYONE_MAX_DAYS);
    expect(capDays(90)).toBe(90);
    expect(latestAnyoneEnd(now, 0).getTime() - now.getTime()).toBe(365 * DAY);
  });

  it('takes the end of the chosen day', () => {
    const iso = anyoneExpiresAt('2026-11-10', now, 0);
    expect(iso).not.toBeNull();
    expect(new Date(iso as string).getDate()).toBe(10);
    expect(new Date(iso as string).getHours()).toBe(23);
  });

  it('pulls a date past the cap back to the cap rather than refusing it', () => {
    const far = anyoneExpiresAt('2030-01-01', now, 0);
    expect(far).toBe(latestAnyoneEnd(now, 0).toISOString());
    expect(anyoneExpiresAt('2030-01-01', now, 30)).toBe(latestAnyoneEnd(now, 30).toISOString());
  });

  it('refuses an empty, malformed or past date', () => {
    expect(anyoneExpiresAt('', now, 0)).toBeNull();
    expect(anyoneExpiresAt('next week', now, 0)).toBeNull();
    expect(anyoneExpiresAt('2026-10-01', now, 0)).toBeNull();
  });
});

describe('passwordProblem', () => {
  it('counts characters, as the server does, and allows no password at all', () => {
    expect(passwordProblem('', 8)).toBeNull();
    expect(passwordProblem('1234567', 8)).toBe('short');
    expect(passwordProblem('12345678', 8)).toBeNull();
    expect(passwordProblem('ש'.repeat(8), 8)).toBeNull();
    expect(passwordProblem('x'.repeat(257), 8)).toBe('long');
    expect(passwordProblem('x'.repeat(256), 8)).toBeNull();
  });
});

describe('outcomeOf and visitStep', () => {
  it('reads each refusal the open can give', () => {
    expect(outcomeOf(new ApiError(401, 'unauthenticated', { reason: 'password_required' }, 0))).toBe('password_required');
    expect(outcomeOf(new ApiError(401, 'unauthenticated', { reason: 'password_wrong' }, 0))).toBe('password_wrong');
    expect(outcomeOf(new ApiError(429, 'rate_limited', {}, 60))).toBe('rate_limited');
    expect(outcomeOf(new ApiError(404, 'not_found', {}, 0))).toBe('not_found');
    expect(outcomeOf(new ApiError(500, 'internal', {}, 0))).toBe('failed');
    expect(outcomeOf(new Error('boom'))).toBe('failed');
  });

  it('sends a signed-out visitor who hits a dead link to sign in, and a signed-in one to the dead message', () => {
    expect(visitStep('not_found', false)).toBe('sign-in');
    expect(visitStep('not_found', true)).toBe('dead');
  });

  it('maps the other outcomes to their page', () => {
    expect(visitStep('opened', false)).toBe('bearer');
    expect(visitStep('password_required', false)).toBe('password');
    expect(visitStep('password_wrong', false)).toBe('password-wrong');
    expect(visitStep('rate_limited', true)).toBe('limited');
    expect(visitStep('failed', true)).toBe('failed');
  });
});

describe('renewDelay', () => {
  const at = now.getTime() + 15 * 60 * 1000;
  it('waits until two minutes before the credential ends', () => {
    expect(renewDelay(new Date(at).toISOString(), now.getTime())).toBe(13 * 60 * 1000);
  });

  it('renews at once when the credential is already that close, and for a bad time', () => {
    expect(renewDelay(new Date(now.getTime() + 60_000).toISOString(), now.getTime())).toBe(0);
    expect(renewDelay('not a time', now.getTime())).toBe(0);
  });
});

describe('crumbsTo', () => {
  const nodes = new Map<string, NodeRef>([
    ['nod_root', { id: 'nod_root', parent_id: null, name: 'Plans' }],
    ['nod_a', { id: 'nod_a', parent_id: 'nod_root', name: 'Drafts' }],
    ['nod_b', { id: 'nod_b', parent_id: 'nod_a', name: 'May' }],
    ['nod_outside', { id: 'nod_outside', parent_id: 'nod_up', name: 'Other' }],
  ]);

  it('lists the path from the link root down, root first', () => {
    expect(crumbsTo(nodes, 'nod_b', 'nod_root')?.map((n) => n.name)).toEqual(['Plans', 'Drafts', 'May']);
  });

  it('is only the root at the root', () => {
    expect(crumbsTo(nodes, 'nod_root', 'nod_root')?.map((n) => n.name)).toEqual(['Plans']);
  });

  it('never goes above the root', () => {
    // nod_up is not in the map: the walk cannot reach the root, so there is no path to show.
    expect(crumbsTo(nodes, 'nod_outside', 'nod_root')).toBeNull();
    // An unknown current node gives no path either.
    expect(crumbsTo(nodes, 'nod_missing', 'nod_root')).toBeNull();
  });
});

describe('previewKind', () => {
  it('shows images, video, audio and PDFs inline, and nothing else', () => {
    expect(previewKind('image/png')).toBe('image');
    expect(previewKind('video/mp4')).toBe('video');
    expect(previewKind('audio/ogg; codecs=opus')).toBe('audio');
    expect(previewKind('application/pdf')).toBe('pdf');
    expect(previewKind('text/plain')).toBeNull();
    expect(previewKind(undefined)).toBeNull();
  });
});
