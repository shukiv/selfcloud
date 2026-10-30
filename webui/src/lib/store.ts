// SPDX-License-Identifier: AGPL-3.0-or-later

import { useSyncExternalStore } from 'react';

// A small store with the same surface the modules were written against: writable, derived, get, and
// a hook that re-renders a component when the value changes. The subscribe contract is the one
// the state modules rely on: a subscriber is called at once with the current value, and again on
// every change, and the function subscribe returns stops it.

export type Subscriber<T> = (value: T) => void;
export type Unsubscriber = () => void;

export interface Readable<T> {
  subscribe(run: Subscriber<T>): Unsubscriber;
}

export interface Writable<T> extends Readable<T> {
  set(value: T): void;
  update(fn: (value: T) => T): void;
}

// readers maps a store to a function that returns its current value without subscribing. The
// stores below register themselves here, so get and the hook read the value directly.
const readers = new WeakMap<object, () => unknown>();

// changed is true when a new value needs to reach subscribers. Objects always count as changed, as
// they do in Svelte, because a mutated object would otherwise never notify anyone.
function changed(a: unknown, b: unknown): boolean {
  return a !== b || (typeof a === 'object' && a !== null) || typeof a === 'function';
}

// userResets holds a reset for every store that carries one person's data. resetUserStores runs them when the
// session changes, so the next person on the tab never sees the last one's spaces, storage or requests.
const userResets: Array<() => void> = [];

// userWritable is writable for data that belongs to the signed-in person. It is emptied on every change of session.
export function userWritable<T>(initial: T): Writable<T> {
  const store = writable(initial);
  userResets.push(() => store.set(initial));
  return store;
}

// resetUserStores empties every store made with userWritable.
export function resetUserStores(): void {
  for (const reset of userResets) reset();
}

export function writable<T>(initial: T): Writable<T> {
  let value = initial;
  const subs = new Set<Subscriber<T>>();
  const store: Writable<T> = {
    subscribe(run) {
      subs.add(run);
      run(value);
      return () => {
        subs.delete(run);
      };
    },
    set(next) {
      if (!changed(value, next)) return;
      value = next;
      for (const run of [...subs]) run(value);
    },
    update(fn) {
      store.set(fn(value));
    },
  };
  readers.set(store, () => value);
  return store;
}

export function derived<S, T>(source: Readable<S>, fn: (value: S) => T): Readable<T> {
  // The last source value and its result. The result is returned again while the source is the same
  // object, so a reader such as useSyncExternalStore sees one value, not a new object on every read.
  let memo: { source: S; value: T } | null = null;
  const of = (s: S): T => {
    if (memo === null || !Object.is(memo.source, s)) memo = { source: s, value: fn(s) };
    return memo.value;
  };
  const store: Readable<T> = {
    subscribe(run) {
      return source.subscribe((v) => run(of(v)));
    },
  };
  readers.set(store, () => of(get(source)));
  return store;
}

// get returns the current value of a store.
export function get<T>(store: Readable<T>): T {
  const read = readers.get(store);
  if (read) return read() as T;
  let value: T | undefined;
  store.subscribe((v) => {
    value = v;
  })();
  return value as T;
}

// useStore returns the current value of a store, and re-renders the component when it changes.
export function useStore<T>(store: Readable<T>): T {
  return useSyncExternalStore(
    (onChange) => store.subscribe(() => onChange()),
    () => get(store),
    () => get(store),
  );
}
