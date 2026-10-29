# Tenant owner pages

Overrides `MASTER.md` for a tenant's owners. An owner creates members (sub-accounts), creates team
spaces, and shares them and their contents with people and groups through the roles model of
`docs/design.md` section 8: viewer ("Can view"), commenter, contributor ("Can upload"), editor ("Can
edit") and manager. Owners also use the drive themselves (see `user.md`).

Members never see these pages. An owner session reaches them; any other session gets the not-found
view. API: `/api/v1/accounts/{acc}/users…`, `/accounts/{acc}/folders…`, `/accounts/{acc}/groups…`,
`/accounts/{acc}/usage`, `/accounts/{acc}/audit`, `/folders/{fld}/settings`, `/nodes/{nod}/permissions`,
`/directory`.

## Navigation

Side navigation, two groups:

- **Drive:** the owner's own browsing, as in `user.md`, with the team spaces listed under it.
- **Manage:** Overview, People, Groups, Team spaces, Audit log.

Members never see the Manage group. Routes: `/manage`, `/manage/people`, `/manage/groups`,
`/manage/spaces`, `/manage/audit`.

## Overview (`/manage`)

- Usage meter for the tenant, then the counts: people (active, pending), team spaces, files.
- "Recent activity": the last 10 audit entries in plain sentences, with a link to the full log.
- Next steps when the tenant is new: "Invite your first person", "Create a team space", each a
  button, shown until done.

## People (`/manage/people`)

- Title "People", primary action "Invite person".
- Rows: name and email (two lines), role (Owner / Member), status badge (Active, Pending invite,
  Disabled). Cursor paging with "Load more".
- Row menu: Resend invitation (pending), Send password reset (active, by email), Make owner or Make
  member, Disable or Enable, Remove.
- **Invite person** dialog: Email, Name, Role (Member default; Owner with a warning that owners
  manage everyone), Language. Always by email (`verification: email`). Links to pass on are for API
  keys and installation admins only.
- **Person drawer** (side drawer at 768px and up, bottom sheet below): details, status, the team
  spaces and groups they belong to, their recent activity, and the Remove button.
- **Remove** confirmation: "{name} loses access to every team space. Their uploads stay in the team
  spaces." A person whose My files hold files is asked what happens to them: move them to another
  active member's My files, or delete them for good (typed name). The count of files is not shown.
- Removing yourself, or the last active owner, is refused, and the reason is shown from the answer's
  `details.reason`. The same applies to a demotion or a disable that would leave no active owner.

## Groups (`/manage/groups`)

- Title "Groups", primary action "New group".
- Rows: name, member count. Open a group to add or remove members (search with the people combobox).
- Rename and delete (confirmation). Deleting a group removes its permissions at once.

## Team spaces (`/manage/spaces`)

- Title "Team spaces", primary action "New team space".
- Rows: name, Hidden badge where it applies, Share, Settings, Rename, Delete.
- **Share** opens the share dialog on the space's root node, the same dialog as the file browser.
- **Settings** is the space settings dialog (`members_only`, `sharing_managers_only`,
  `no_download_for_viewers`; "Allow links" disabled, "Available later").
- **Delete** confirmation: states that everyone with access loses the space and its files, and
  requires the space name typed. The file count and size are not shown (the API gives no total for a
  folder root).

## Audit log (`/manage/audit`)

Same table as the admin's, scoped to this tenant, readable sentences with the raw action as a mono
secondary line. A filter by action; entries about a personal folder are returned or withheld exactly as
the API answers.

## As built (k4)

- Team space delete shows the file count and the size of the whole tree (from the folder detail), then the
  typed name, as above.
- The audit log filters by action only in the page. The API also takes `actor`; the page does not offer it.
- The person view is a dialog, not a side drawer.
- Overview "Recent activity" lists the last ten entries; the full log is one link away.
- Known gaps are in docs/design.md section 10 (Administration, k4).
