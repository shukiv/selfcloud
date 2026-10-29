# Installation admin pages

Overrides `MASTER.md` for an installation admin (an admin user signed in with a password). An
admin creates tenants (accounts), watches their usage, and manages other admins. API:
`/api/v1/accounts…`, `/api/v1/admins…`, `/api/v1/info`, `/api/v1/jobs/{job}`.

## Navigation

Side navigation: **Tenants**, **Admins**, **Installation**. A small "Admin" badge next to the
product name, so an admin never mistakes this for a tenant's view.

## Tenants (`/admin/tenants`, the admin home)

- Page title "Tenants", primary action "New tenant".
- Search by name or external ID (debounced 250ms, in the URL so it can be bookmarked); filter
  chips: All, Active, Suspended.
- Table columns: Name, External ID (mono, muted, hidden < 1024px), Status badge, Usage (usage
  meter with "12.4 GB of 50 GB" or "12.4 GB, no limit"), Users (count), Created (relative,
  absolute in a tooltip and `title`).
- Row click opens the tenant; a row menu (kebab, keyboard reachable) has Open, Suspend/Resume,
  Delete.
- Cursor pagination: "Load more" at the bottom, not page numbers.
- Empty: "No tenants yet. A tenant is an organisation with its own users and folders." and the
  New tenant button.

## New tenant (dialog)

Fields, top to bottom:

1. Name (required).
2. Storage limit: number plus unit (GB, TB), with a "No limit" switch that disables the number.
3. First owner: Email (required), Name.
4. How the owner gets in: radio "Send an email invitation" (default) or "Show me a link to pass
   on". The link option shows the link once, with a Copy button, after creation.
5. Advanced (collapsed): External ID, with the help text "Your own reference for this tenant,
   for example an ID in another system."

Primary "Create tenant". On success the dialog closes, a toast says "Tenant created", and the
tenant page opens.

## Tenant (`/admin/tenants/{acc}`)

- Header: name (editable inline with a pencil button), status badge, external ID; actions
  Suspend/Resume and a danger-styled "Delete tenant…" in the overflow menu.
- Tabs (`role="tablist"`, arrow keys move between them): **Overview**, **Users**, **API keys**,
  **Audit log**.
  - **Overview:** a large usage meter, then counts (users, folders, files), the storage limit
    with "Change", and created/updated times.
  - **Users:** the tenant's users, read-only list with role and status. Admins do not manage a
    tenant's members day to day; that is the owner's job. They can resend an owner's invitation.
  - **API keys:** name, prefix (mono), created, last used, expires; "New key" opens a dialog with
    Name and Expires (Never, 30 days, 90 days, 1 year). The created key is shown **once** in a
    read-only field with Copy and the warning "Copy it now. You will not see it again.", and the
    dialog cannot be dismissed by clicking outside until the user presses "I have copied it".
    Revoke asks for confirmation and states that apps using the key stop working at once.
  - **Audit log:** time, actor, action, target; filter by action. Newest first.
- **Suspend** confirmation: "Users of {name} will be signed out and cannot sign in until you
  resume it. Files are kept."
- **Delete** confirmation: lists what is lost (users, folders, files, size), requires typing the
  tenant name, and the button reads "Delete {name} permanently". Deletion runs as a job: the
  page shows "Deleting…" and polls `/jobs/{job}` until done, then returns to Tenants.

## Admins (`/admin/admins`)

- Table: name, email, status, last sign-in. "New admin" dialog: Email, Name, invitation method
  as above. Remove admin asks for confirmation; the API refuses removing the last admin, and the
  UI disables that action with the reason as its tooltip and helper text.

## Installation (`/admin/installation`)

- Read-only facts from `/api/v1/info`: version, upload piece size, largest file, public URL,
  web UI on/off. Links to the OpenAPI spec and to `/readyz`.

## As built (k4)

- The tenant list has no users column: the API gives no per-tenant user count yet.
- Tenant search matches the name only, ASCII-only, as the sharing directory does.
- The tenant page keeps its tab in the address (`?tab=`), so a tab survives a reload.
- Installation facts are read-only: version, public URL, interface on or off, piece size, largest file, and links to
  the OpenAPI spec and `/readyz`.
- Known gaps are in docs/design.md section 10 (Administration, k4).
