// SPDX-License-Identifier: AGPL-3.0-or-later

// firstGrapheme is the first user-perceived character of a text: a letter with its marks, or a whole emoji. Where the browser
// cannot segment text, it is the first code point.
function firstGrapheme(text: string): string {
  if (text === '') return '';
  if (typeof Intl !== 'undefined' && 'Segmenter' in Intl) {
    const [first] = new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(text);
    return first?.segment ?? '';
  }
  return Array.from(text)[0] ?? '';
}

// upperFirst uppercases the first character of a grapheme for display, and keeps the rest. A capital that is two characters
// (the German sharp s becomes SS) is not used: the letter is kept as it is. A letter with no capital (Hebrew, Arabic) is kept.
function upperFirst(grapheme: string): string {
  const [head, ...rest] = Array.from(grapheme);
  const upper = head.toLocaleUpperCase();
  return Array.from(upper).length === 1 ? upper + rest.join('') : grapheme;
}

// initialOf is the letter an account avatar shows: the first character of the name, or of the email when there is no name.
// Empty when neither is set. The letter is for display only; a name is never changed.
export function initialOf(name: string | undefined | null, email: string | undefined | null): string {
  for (const text of [name, email]) {
    const first = firstGrapheme((text ?? '').trim());
    if (first !== '') return upperFirst(first);
  }
  return '';
}

// accountLabel is the account button's name and its tooltip: the name, else the email, else the fallback (decision 68).
export function accountLabel(name: string | undefined | null, email: string | undefined | null, fallback: string): string {
  return (name ?? '').trim() || (email ?? '').trim() || fallback;
}
