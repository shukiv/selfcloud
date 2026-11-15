// SPDX-License-Identifier: AGPL-3.0-or-later

// The list's row height choice (decision 194): Compact or Comfortable, kept per signed-in person in this browser. There is
// no server-side preference store, so the storage key carries the person's ID and two people on one browser keep their own
// choice. One in-memory value per person with a subscribe list (the pattern of tags.ts), so every mounted list re-renders
// when the choice changes. Storage can be blocked, so every access is guarded as viewPrefs' is, and the default applies
// when nothing can be read.

import { useCallback, useSyncExternalStore } from 'react';
import { session } from '../session';
import { useStore } from '../store';

export type Density = 'comfortable' | 'compact';

export const DENSITY_KEY_PREFIX = 'selfcloud.density.';

const DEFAULT_DENSITY: Density = 'comfortable';

// The choice per person, and the listeners that show it. A value is kept once read or written, so a blocked storage is not
// asked again and every list reads the same answer.
const values = new Map<string, Density>();
const listeners = new Set<() => void>();

function publish(): void {
  for (const l of listeners) l();
}

// subscribeDensity is the store the lists read through useSyncExternalStore.
export function subscribeDensity(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

// readDensity is the person's choice: comfortable by default, and for garbage or blocked storage. No person has no choice.
export function readDensity(userId: string | null): Density {
  if (userId === null) return DEFAULT_DENSITY;
  const kept = values.get(userId);
  if (kept !== undefined) return kept;
  let d: Density = DEFAULT_DENSITY;
  try {
    if (window.localStorage.getItem(DENSITY_KEY_PREFIX + userId) === 'compact') d = 'compact';
  } catch {
    // Storage is blocked: the default applies.
  }
  values.set(userId, d);
  return d;
}

// writeDensity keeps the person's choice and tells every mounted list. No person stores nothing. A blocked storage is
// swallowed: the choice lasts for this page.
export function writeDensity(userId: string | null, d: Density): void {
  if (userId === null) return;
  values.set(userId, d);
  try {
    window.localStorage.setItem(DENSITY_KEY_PREFIX + userId, d);
  } catch {
    // Not saved; the choice lasts for this page.
  }
  publish();
}

// useDensity is the signed-in person's choice and the way to set it. A change of person reads the other's choice, never the
// last one's; a sign-out reads the default.
export function useDensity(): [Density, (d: Density) => void] {
  const s = useStore(session);
  const userId = s === null ? null : s.user.id;
  const density = useSyncExternalStore(subscribeDensity, () => readDensity(userId));
  const set = useCallback((d: Density) => writeDensity(userId, d), [userId]);
  return [density, set];
}
