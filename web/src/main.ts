// SPDX-License-Identifier: AGPL-3.0-or-later

import { mount } from 'svelte';
import '@fontsource-variable/inter';
import '@fontsource-variable/noto-sans-hebrew';
import '@fontsource-variable/noto-sans-arabic';
import './app.css';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) throw new Error('the page has no #app element');
mount(App, { target });
