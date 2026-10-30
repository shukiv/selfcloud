// SPDX-License-Identifier: AGPL-3.0-or-later

/// <reference types="vitest/config" />
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// The API server the dev server forwards /api and /brand to. Set SELFCLOUD_API to point elsewhere.
const api = process.env.SELFCLOUD_API ?? "http://127.0.0.1:8080";

// The Go server replaces this placeholder with a fresh nonce on every HTML response. It must
// match the value internal/web looks for (cspNoncePlaceholder).
export const CSP_NONCE_PLACEHOLDER = "__SELFCLOUD_CSP_NONCE__";

// rc-util writes its scroll-lock and measurement styles with no nonce. Every import of its dynamicCSS
// helper, bare or relative and written without the .js extension, is sent to the shim in
// src/csp/dynamicCSS.ts, which fills the nonce in from the page's meta tag. The shim imports the
// original with the .js extension, which this plugin does not match, so the shim never redirects to
// itself.
const dynamicCSS = fileURLToPath(new URL("./src/csp/dynamicCSS.ts", import.meta.url));
const shimDynamicCSS: Plugin = {
  name: "selfcloud-rc-util-dynamic-css",
  enforce: "pre",
  resolveId(source) {
    return /(^|\/)Dom\/dynamicCSS$/.test(source) ? dynamicCSS : null;
  },
};

export default defineConfig({
  plugins: [react(), shimDynamicCSS],
  publicDir: "public",
  html: { cspNonce: CSP_NONCE_PLACEHOLDER },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
    // Nothing is inlined: the policy has no 'unsafe-inline' and no data: fonts.
    assetsInlineLimit: 0,
    // The module preload polyfill is an inline script, which the policy forbids.
    modulePreload: { polyfill: false },
    // The library core (React, the router, the query client, i18next) goes into one vendor chunk, so the chunk
    // that holds the antd pieces both the shell and the lazy pages use stays under the 120 KB budget (decision 25).
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (!id.includes("node_modules")) return undefined;
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler|react-router|react-router-dom|@remix-run|@tanstack|i18next|react-i18next)[\\/]/.test(id)) return "vendor";
          return undefined;
        },
      },
    },
  },
  server: {
    proxy: {
      "/api": api,
      "/brand": api,
    },
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    testTimeout: 20000,
  },
});
