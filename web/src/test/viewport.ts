// SPDX-License-Identifier: AGPL-3.0-or-later

// setViewport makes window.matchMedia answer for a window of the given width, so a test sees the desktop layout (the inline
// icons and the checkbox column) or the phone one (the ⋮ menu alone, the secondary columns as the name's second line). It
// answers the min-width and max-width queries the app asks; the other queries match nothing. It returns the restore function.
export function setViewport(width: number): () => void {
  const saved = window.matchMedia;
  window.matchMedia = ((query: string) => {
    const min = /min-width:\s*(\d+)px/.exec(query);
    const max = /max-width:\s*(\d+)px/.exec(query);
    const matches = min ? width >= Number(min[1]) : max ? width <= Number(max[1]) : false;
    return { matches, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  }) as unknown as typeof window.matchMedia;
  return () => {
    window.matchMedia = saved;
  };
}

// DESKTOP is a window of 1200 pixels, PHONE one of 375.
export const DESKTOP = 1200;
export const PHONE = 375;

// desktopViewport makes every min-width query match, as a desktop window does: a list then draws its row icons inline, where a
// phone (below md, 768 px) keeps only the ⋮ menu (decision 122). It returns the function that restores the setup's stub.
export function desktopViewport(): () => void {
  const before = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: /min-width: \d+px/.test(query),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return () => {
    window.matchMedia = before;
  };
}

// phoneViewport makes no min-width query match: a phone below md (768 px), where a list shows its name column, its secondary
// columns on the name's second line, and the ⋮ menu alone (design section 4). It returns the function that restores the
// window the test had before it, so a test can call it inside itself: `const undo = phoneViewport(); try { ... } finally { undo(); }`.
export function phoneViewport(): () => void {
  const before = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return () => {
    window.matchMedia = before;
  };
}
