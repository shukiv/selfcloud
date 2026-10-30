// SPDX-License-Identifier: AGPL-3.0-or-later

// A stand-in for rc-util's dynamicCSS module, aliased in place of it by vite.config.ts. rc-util
// writes the styles of the scroll lock and the scrollbar measure without a nonce, which the strict
// policy would block. This version fills the nonce from the page's meta tag when a caller passes
// none. The original module is imported under its file name, which the alias does not match.
import * as original from 'rc-util/es/Dom/dynamicCSS.js';
import { readNonce } from './nonce';

type Options = Parameters<typeof original.injectCSS>[1];

function withNonce(option: Options): Options {
  const nonce = option?.csp?.nonce || readNonce();
  if (!nonce) return option;
  return { ...option, csp: { ...option?.csp, nonce } };
}

export const injectCSS: typeof original.injectCSS = (css, option) => original.injectCSS(css, withNonce(option));

export const updateCSS: typeof original.updateCSS = (css, key, option) =>
  original.updateCSS(css, key, withNonce(option));

export const removeCSS: typeof original.removeCSS = (key, option) => original.removeCSS(key, option);

export const clearContainerCache: typeof original.clearContainerCache = () => original.clearContainerCache();
