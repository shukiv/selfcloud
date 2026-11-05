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

// The key is "v2" since decision 69: the tree stopped opening folders on its own, and the open folders saved under the old key are not
// read, so every person starts with the tree folded once. Since decision 88 the tree opens the way to the folder being viewed, and
// those folders are saved here like any folder the person opened by hand; the key stays "v2" so no saved state is reset.
const key = (user: string): string => `selfcloud.tree.v2.${user}`;

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
