// SPDX-License-Identifier: AGPL-3.0-or-later

// What to do when a name is already taken in an upload or a move: replace the file, keep both (the
// new one takes a free name), or skip it.
export type ConflictChoice = 'replace' | 'rename' | 'skip';

export interface ConflictState {
  // batch is the choice the person made for the whole batch, or null while none is made.
  batch: ConflictChoice | null;
  // asking is the name the interface is asking about, or null.
  asking: string | null;
}

export type ConflictAction =
  | { type: 'conflict'; name: string }
  | { type: 'answer'; choice: ConflictChoice; forBatch: boolean };

export type ConflictEffect = { kind: 'ask' } | { kind: 'apply'; choice: ConflictChoice } | { kind: 'none' };

export const INITIAL_CONFLICT: ConflictState = { batch: null, asking: null };

// conflictReducer decides one conflict. With a batch choice made it applies that choice without asking;
// otherwise it asks once, and the next answer decides whether the choice holds for the rest of the batch.
export function conflictReducer(
  state: ConflictState,
  action: ConflictAction,
): { state: ConflictState; effect: ConflictEffect } {
  if (action.type === 'conflict') {
    if (state.batch) return { state, effect: { kind: 'apply', choice: state.batch } };
    return { state: { ...state, asking: action.name }, effect: { kind: 'ask' } };
  }
  return {
    state: { batch: action.forBatch ? action.choice : state.batch, asking: null },
    effect: { kind: 'apply', choice: action.choice },
  };
}
