// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable, get } from 'svelte/store';

export interface Location {
  path: string;
  search: string;
}

function here(): Location {
  return { path: window.location.pathname, search: window.location.search };
}

// location follows the address bar, so the app re-renders on navigation.
export const location = writable<Location>(here());

// go navigates inside the app: it changes the address without a page load.
export function go(url: string, opts: { replace?: boolean } = {}): void {
  if (opts.replace) {
    window.history.replaceState(null, '', url);
  } else {
    window.history.pushState(null, '', url);
  }
  location.set(here());
}

window.addEventListener('popstate', () => location.set(here()));

// signInURL is the sign-in page, remembering where to go back to. The home page needs no
// remembering.
export function signInURL(from: string = get(location).path + get(location).search): string {
  if (from === '/' || from === '' || from.startsWith('/login')) return '/login';
  return '/login?next=' + encodeURIComponent(from);
}
