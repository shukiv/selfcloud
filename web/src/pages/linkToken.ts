// SPDX-License-Identifier: AGPL-3.0-or-later

// readFragmentToken reads the token a mailed link carries in its fragment, which never reaches a
// server log, and removes the fragment from the address bar. Call it once per page load.
export function readFragmentToken(): string {
  let token = "";
  try {
    token = new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
  } catch {
    token = "";
  }
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
  return token;
}
