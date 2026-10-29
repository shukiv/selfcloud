// SPDX-License-Identifier: AGPL-3.0-or-later

import { mount } from 'svelte';
import '@fontsource-variable/inter';
import '@fontsource-variable/noto-sans-hebrew';
import '@fontsource-variable/noto-sans-arabic';
import './app.css';
import App from './App.svelte';
import { captureSecret } from './lib/sharing/links';

// A shared link keeps its secret in the address fragment. Move it into this tab's session storage, and strip
// the address, before anything else runs.
if (window.location.pathname === '/s') {
  captureSecret(window.location.hash, window.location.pathname, window.location.search, window.sessionStorage,
    (url) => window.history.replaceState(null, '', url));
}

const target = document.getElementById('app');
if (!target) throw new Error('the page has no #app element');
mount(App, { target });
