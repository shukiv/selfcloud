// SPDX-License-Identifier: AGPL-3.0-or-later

/// <reference types="vitest/config" />
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
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

// The service worker (public/sw.js) names its cache with an identifier of the build, so a new build starts a new cache and the
// worker removes the old one (decision 65). The identifier is a hash of the built file names, the offline files and the
// worker's own source, so the same build gives the same name. The worker file is copied as it is; only this placeholder is filled in.
const WORKER_PLACEHOLDER = "__SELFCLOUD_BUILD__";
const workerBuild: Plugin = {
  name: "selfcloud-service-worker-build",
  apply: "build",
  closeBundle() {
    const dist = fileURLToPath(new URL("./dist/", import.meta.url));
    const worker = join(dist, "sw.js");
    if (!existsSync(worker)) throw new Error("dist/sw.js is missing: the service worker is not in the build");
    // The identifier covers what the worker serves from its cache: the build's file names, and the contents of the offline page,
    // its files and the favicon, so a change to any of them gives the worker a new cache and a new copy to fetch.
    const names = readdirSync(join(dist, "assets")).sort();
    const hash = createHash("sha256").update(names.join("\n")).update(readFileSync(join(dist, "sw.js"), "utf8"));
    for (const file of ["offline.html", "offline.css", "offline.js", "favicon.svg"]) {
      hash.update(file).update("\0").update(readFileSync(join(dist, file)));
    }
    const id = hash.digest("hex").slice(0, 16);
    const text = readFileSync(worker, "utf8");
    if (!text.includes(WORKER_PLACEHOLDER)) throw new Error("public/sw.js has no build placeholder");
    writeFileSync(worker, text.split(WORKER_PLACEHOLDER).join(id));
  },
};

export default defineConfig({
  plugins: [react(), shimDynamicCSS, workerBuild],
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
        // Rolldown's groups (the successor of manualChunks). A group also takes the dependencies of the modules it captures,
        // unless includeDependenciesRecursively is false. antd-shared is matched through the modal and reached React that way,
        // so vendor has the higher priority and takes React first; a module a higher group takes is not taken again.
        // The csp group keeps the src/csp files out of the library chunks, and takes the rc-util module the shim wraps with
        // its dependencies, so the group imports nothing from antd-shared and no chunk cycle forms (decision 99).
        codeSplitting: {
          groups: [
            {
              name: "csp",
              priority: 3,
              test: (id: string) => /[\\/]src[\\/]csp[\\/]|[\\/]node_modules[\\/]rc-util[\\/]es[\\/]Dom[\\/]dynamicCSS\.js$/.test(id),
            },
            {
              name: "vendor",
              priority: 2,
              test: (id: string) => /[\\/]node_modules[\\/](react|react-dom|scheduler|react-router|react-router-dom|@remix-run|@tanstack|i18next|react-i18next)[\\/]/.test(id),
            },
            {
              name: "antd-shared",
              // The antd modal and skeleton and rc-util's useId, which the first load reached through the main i18n chunk, go
              // into one shared chunk of their own, so the first load preloads one file for them and not five (r7b review).
              test: (id: string) => /[\\/]node_modules[\\/](antd[\\/]es[\\/](modal|skeleton|input[\\/]style|checkbox[\\/]style|style[\\/]motion[\\/]move)[\\/]|rc-util[\\/]es[\\/]hooks[\\/]useId\.js|rc-dropdown[\\/]|rc-menu[\\/]|@ant-design[\\/]icons[\\/]es[\\/]icons[\\/](EllipsisOutlined|LeftOutlined)\.js)/.test(id),
            },
          ],
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
