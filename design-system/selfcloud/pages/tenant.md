# Tenant owner pages

Overrides `MASTER.md` for a tenant's owners. An owner creates sub-accounts (member users),
creates top-level folders and shares each folder with members as **Can view** (read) or **Can
edit** (write). Owners also use the drive themselves (see `user.md`). API:
`/api/v1/accounts/{acc}/users…`, `/accounts/{acc}/folders…`, `/folders/{fld}/grants…`,
`/accounts/{acc}/usage`, `/accounts/{acc}/audit`.

## Navigation

Side navigation, two groups:

- **Drive:** Folders (the owner's own browsing, as in `user.md`).
- **Manage:** Overview, People, Shared folders, Audit log.

Members never see the Manage group.

## Overview (`/manage`)

- Usage meter for the tenant, then three figures: people (active / pending), folders, files.
- "Recent activity": the last 10 audit entries in plain sentences ("Dana shared Finance with
  Omer, Can edit"), with a link to the full log.
- Next steps when the tenant is new: "Invite your first person", "Create a folder", each a
  button, shown until done.

## People (`/manage/people`)

- Title "People", primary action "Invite person".
- Table: Name and email (two lines), Role (Owner / Member), Status badge (Active, Pending
  invite, Disabled), Folders (count, links to the person's drawer), Last sign-in.
- Row menu: Share folders…, Resend invitation (pending only), Send password reset (active only,
  by email), Make owner / Make member, Disable / Enable, Remove….
- **Invite person** dialog: Email, Name, Role (Member default; Owner with a warning that owners
  can manage everyone). Optional section "Share folders now": a list of folders, each with an
  access segmented control (None, Can view, Can edit). Primary "Send invitation". The invitation
  is always by email for owners (a link to pass on is only for API keys and admins).
- **Person drawer** (side drawer ≥ 768px, bottom sheet below): details, status, the folders
  they can reach with their level and a Remove button per row, "Add folder", and the activity of
  this person.
- **Remove person** confirmation: "{name} loses access to every shared folder. Their uploads stay
  in the folders." Removing yourself or the last owner is refused, with the reason shown.

## Shared folders (`/manage/folders`)

- Title "Shared folders", primary action "New folder".
- Table: Folder name, People (avatar stack with "+3", the number spelled out for screen
  readers), Size, Updated.
- Row click opens the **Share** dialog; the row menu also has Open in drive, Rename, Delete….
- **Share dialog**, titled "Share {folder}":
  - an "Add people" combobox searching the tenant's people by name or email, plus the access
    control (Can view / Can edit) and "Add";
  - a list of who has access: owners shown first as "Owner, full access" (not editable), then
    each member with an access dropdown (Can view, Can edit, Remove);
  - an explanation line under the title: "Can view: open and download. Can edit: also upload,
    rename, move and delete.";
  - changes save immediately per row, with a toast and Undo.
- **Delete folder** confirmation lists file count and size, requires typing the folder name, and
  notes that everyone with access loses it.

## Audit log (`/manage/audit`)

Same table as the admin's, scoped to this tenant, readable sentences with the raw action as a
mono secondary line.
