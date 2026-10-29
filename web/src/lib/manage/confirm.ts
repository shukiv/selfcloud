// SPDX-License-Identifier: AGPL-3.0-or-later

// typedNameMatches is true when the typed text is the name, ignoring the spaces at its ends. The match is
// otherwise exact, so "Finance" and "finance" differ: a name typed to confirm a deletion must be the name.
export function typedNameMatches(typed: string, name: string): boolean {
  return name.length > 0 && typed.trim() === name;
}
