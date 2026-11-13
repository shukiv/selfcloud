// SPDX-License-Identifier: AGPL-3.0-or-later

// LONG_LINE is the length past which a line turns wrapping off (decision 115): a minified file wrapped on a phone would be
// thousands of rows to lay out.
export const LONG_LINE = 2000;

// wraps says the viewer wraps the text's lines: yes, unless a line is longer than LONG_LINE characters. The line breaks are
// LF, CRLF and CR.
export function wraps(text: string): boolean {
  let start = 0;
  for (let i = 0; i <= text.length; i++) {
    const c = i < text.length ? text.charCodeAt(i) : 10;
    if (c !== 10 && c !== 13) continue;
    if (i - start > LONG_LINE) return false;
    start = i + 1;
  }
  return true;
}
