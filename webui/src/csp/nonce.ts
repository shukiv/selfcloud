// SPDX-License-Identifier: AGPL-3.0-or-later

// The server writes the page's nonce into a meta tag on every HTML response. Browsers hide a nonce
// attribute once the element is parsed, so the value is read from the IDL property, and the
// attribute only where the property is missing.
export function readNonce(): string {
  if (typeof document === 'undefined') return '';
  const meta = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]');
  if (!meta) return '';
  return meta.nonce || meta.getAttribute('nonce') || '';
}
