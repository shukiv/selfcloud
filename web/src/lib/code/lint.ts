// SPDX-License-Identifier: AGPL-3.0-or-later

// translateLint gives every problem the JSON parser found the one translated message, and keeps each problem's place and severity.
// The parser's own words (for example "Unexpected token") are English and engine-specific, so they are not shown.
export function translateLint<T extends { message: string }>(found: T[], message: string): T[] {
  return found.map((d) => ({ ...d, message }));
}
