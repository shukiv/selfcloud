// SPDX-License-Identifier: AGPL-3.0-or-later

// personLabel names who did something to a file. The signed-in person's own ID shows their name; an account
// key's ID shows "Account key"; another user's ID has no name in any answer the interface gets, so it gives
// null and the caller leaves the row out. A raw ID is never shown.
export interface Viewer {
  user: { id: string; display_name?: string };
}

export function personLabel(id: string | null | undefined, viewer: Viewer | null, t: (key: string) => string): string | null {
  if (!id) return null;
  if (viewer && id === viewer.user.id) return viewer.user.display_name?.trim() || t("files.details.you");
  if (id.startsWith("key_")) return t("files.details.accountKey");
  return null;
}

// detailsPersonLabel is personLabel for Details, which also takes the display name the node reply carries for another
// person of the account (created_by_name, owner_name). The API sends a name only to a caller who reaches the node, so
// the name is shown as it is; with no name, the row is left out, as personLabel leaves it.
export function detailsPersonLabel(
  id: string | null | undefined,
  name: string | null | undefined,
  viewer: Viewer | null,
  t: (key: string) => string,
): string | null {
  const label = personLabel(id, viewer, t);
  if (label !== null) return label;
  return name?.trim() || null;
}
