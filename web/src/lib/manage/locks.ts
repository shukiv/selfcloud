// SPDX-License-Identifier: AGPL-3.0-or-later

// LOCK names the lock of each row action on the management pages (useOncePer). A key has the action's kind and the
// parent it acts within, so one row's two different actions do not share a lock, and one user in two groups does not
// either. A key that is only the row's ID would block the other action, or the same user in another group.
export const LOCK = {
  person: (usr: string) => `person:${usr}`,
  groupAdd: (grp: string) => `group-add:${grp}`,
  groupDelete: (grp: string) => `group-delete:${grp}`,
  groupMember: (grp: string, usr: string) => `group-member:${grp}:${usr}`,
  spaceShare: (fld: string) => `space-share:${fld}`,
  spaceOpenDelete: (fld: string) => `space-open-delete:${fld}`,
  spaceDelete: (fld: string) => `space-delete:${fld}`,
  linkDelete: (lnk: string) => `link-delete:${lnk}`,
} as const;
