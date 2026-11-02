// SPDX-License-Identifier: AGPL-3.0-or-later

// refusedOnPurpose says whether a browser log entry is a 415 the s4b section caused on purpose: a conversion of a node that
// the section refused on purpose (a failed export, a refused preview, a file type the converter does not take). Any other 415,
// or a 415 on a node the section did not refuse, is not expected and fails the run.
//
// entry: a browser log entry with source, url and text. refusedIDs: the node IDs the section has refused, filled in before each
// refusal happens.
export function refusedOnPurpose(entry, refusedIDs) {
  if (entry.source !== 'network') return false;
  if (!/status of 415\b/.test(entry.text ?? '')) return false;
  const m = /\/api\/v1\/nodes\/([^/?#]+)\/(?:preview|exports)(?:[/?#]|$)/.exec(entry.url ?? '');
  return m !== null && refusedIDs.has(m[1]);
}
