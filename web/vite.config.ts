// SPDX-License-Identifier: AGPL-3.0-or-later

/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The API server the dev server forwards /api and /brand to. Set SELFCLOUD_API to point elsewhere.
const api = process.env.SELFCLOUD_API ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [svelte()],
  publicDir: 'public',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
    // Nothing is inlined: the policy has no 'unsafe-inline' and no data: fonts.
    assetsInlineLimit: 0,
    // The module preload polyfill is an inline script, which the policy forbids.
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: {
      '/api': api,
      '/brand': api,
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
});
