// SPDX-License-Identifier: AGPL-3.0-or-later

import { renderHook, act } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { derived, get, useStore, writable } from './store';

describe('derived', () => {
  it('reads the same value twice when the source has not changed', () => {
    const src = writable({ a: 1 });
    const d = derived(src, (v) => ({ x: v.a }));
    expect(get(d)).toBe(get(d));
  });

  it('computes again only when the source changes', () => {
    const src = writable(1);
    let calls = 0;
    const d = derived(src, (v) => {
      calls++;
      return { v };
    });
    get(d);
    get(d);
    expect(calls).toBe(1);
    src.set(2);
    expect(get(d)).toEqual({ v: 2 });
    expect(calls).toBe(2);
  });
});

describe('useStore on a derived store', () => {
  it('renders the value and updates it, without looping', () => {
    const src = writable({ a: 1 });
    const d = derived(src, (v) => ({ doubled: v.a * 2 }));
    const { result } = renderHook(() => useStore(d));
    expect(result.current).toEqual({ doubled: 2 });
    act(() => src.set({ a: 4 }));
    expect(result.current).toEqual({ doubled: 8 });
  });
});
