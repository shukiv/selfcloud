// SPDX-License-Identifier: AGPL-3.0-or-later

import { createBrowserRouter, type RouteObject } from 'react-router-dom';
import { writable, get } from './store';

export interface Location {
  path: string;
  search: string;
}

function here(): Location {
  return { path: window.location.pathname, search: window.location.search };
}

// location follows the address bar. The app's router updates it on every navigation.
export const location = writable<Location>(here());

// navigate is the router's navigate, once createAppRouter has bound it. Until then go changes the
// address with the History API, so code that runs before the app mounts still works.
let navigate: ((to: string, opts: { replace?: boolean }) => void) | null = null;

// createAppRouter builds the data router for the app. It keeps location in step with the address
// and lets go navigate from outside a component.
export function createAppRouter(routes: RouteObject[]): ReturnType<typeof createBrowserRouter> {
  const router = createBrowserRouter(routes);
  navigate = (to, opts) => {
    void router.navigate(to, { replace: opts.replace === true });
  };
  router.subscribe((state) => {
    location.set({ path: state.location.pathname, search: state.location.search });
  });
  return router;
}

// go navigates inside the app: it changes the address without a page load.
export function go(url: string, opts: { replace?: boolean } = {}): void {
  if (navigate) {
    navigate(url, opts);
    return;
  }
  if (opts.replace) {
    window.history.replaceState(null, '', url);
  } else {
    window.history.pushState(null, '', url);
  }
  location.set(here());
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('popstate', () => {
    if (!navigate) location.set(here());
  });
}

// signInURL is the sign-in page, remembering where to go back to. The home page needs no
// remembering.
export function signInURL(from: string = get(location).path + get(location).search): string {
  if (from === '/' || from === '' || from.startsWith('/login')) return '/login';
  return '/login?next=' + encodeURIComponent(from);
}
