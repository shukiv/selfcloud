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

// DESKTOP is a window of 1200 pixels, PHONE one of 375 (design-tl section 4).
export const DESKTOP = 1200;
export const PHONE = 375;
