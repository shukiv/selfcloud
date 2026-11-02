// SPDX-License-Identifier: AGPL-3.0-or-later

// What the folder tree in the sidebar remembers for a person: which folders are opened in it. It is kept in the browser
// under the person's own id, so another person on the same browser has theirs. Storage can be blocked, so every access is
// guarded; the defaults apply when nothing can be read.
export interface TreeState {
  expanded: string[];
}

export const DEFAULT_TREE: TreeState = { expanded: [] };

// MAX_EXPANDED keeps a long-lived list from growing for ever: the folders opened longest ago are forgotten first.
export const MAX_EXPANDED = 60;

const key = (user: string): string => `selfcloud.tree.${user}`;

// parseTree reads stored state, ignoring anything it does not recognise. A state saved when the tree had its own column
// may carry an "open" flag; it is ignored.
export function parseTree(raw: string | null): TreeState {
  if (!raw) return DEFAULT_TREE;
  try {
    const v = JSON.parse(raw) as Partial<TreeState>;
    const expanded = Array.isArray(v.expanded) ? v.expanded.filter((x): x is string => typeof x === "string").slice(-MAX_EXPANDED) : [];
    return { expanded };
  } catch {
    return DEFAULT_TREE;
  }
}

export function readTree(user: string): TreeState {
  try {
    return parseTree(window.localStorage.getItem(key(user)));
  } catch {
    return DEFAULT_TREE;
  }
}

export function writeTree(user: string, state: TreeState): void {
  try {
    window.localStorage.setItem(key(user), JSON.stringify(state));
  } catch {
    // Not saved; the choice lasts for this page.
  }
}

// withExpanded opens or closes one folder. An opened one goes to the end of the list, as the most recent.
export function withExpanded(state: TreeState, id: string, on: boolean): TreeState {
  const rest = state.expanded.filter((x) => x !== id);
  return { ...state, expanded: on ? [...rest, id].slice(-MAX_EXPANDED) : rest };
}

// revealed opens the folders on the way to the place the person is in, so the tree shows where they are. It returns the
// same object when they are all open already.
export function revealed(state: TreeState, ids: string[]): TreeState {
  const missing = ids.filter((id) => !state.expanded.includes(id));
  if (missing.length === 0) return state;
  return { ...state, expanded: [...state.expanded, ...missing].slice(-MAX_EXPANDED) };
}
