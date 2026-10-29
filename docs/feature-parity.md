# Feature parity with Google Drive

Status: **approved 2026-10-28** (design.md §15, decisions 11 to 13). Search limits: full text up to 50 MiB, OCR up to 10 MiB images and 20 MiB PDFs.

The goal is to offer what Google Drive offers, matched in function, not in look: the screens
follow Selfcloud's own design system. The inventory behind this file covers 213 Drive features
from Google's help centre, admin help and Drive API v3 reference.

Each feature is marked:

- **Have**: built in slices a to g, or in h, which is in progress.
- **Planned**: already in slices i to l of the design.
- **New**: proposed here, in one of the new slices m to u at the end.
- **Skip**: not proposed, with the reason.

## Decisions this reopens

Approved decisions in `design.md` §15 that this proposal changes:

| Decision | Today | Proposed |
|---|---|---|
| 1. Permission levels | `read`, `write` | viewer, commenter, editor, manager |
| 2. Grants | Whole top-level folders only | Any file or folder, inherited down the tree |
| 4. Share links, versions, trash | Not in the first release | All three (slices m, o) |

The data model also changes. Grants become permissions on any node, a user gets a personal
space ("My files"), and a group can hold users.

## 1. Files and upload

| Drive feature | Selfcloud |
|---|---|
| Upload files and folders, with drag and drop | Have: resumable uploads (f). Built (k2): the upload tray, folder upload and drag and drop |
| Create blank documents, spreadsheets and presentations | Planned (l): Collabora, from templates (`TemplateSource`) |
| Create from a template gallery | New (l+): per-account template folders |
| Maximum file size (5 TB in Drive) | Have: settable, default 100 GiB (`upload.max_file`) |
| Daily upload cap per user | New (u): `upload.daily_cap_per_user` |
| Copy-size limit, folder depth limit, items per folder | Have (copy limit); New (u): depth and per-folder caps |
| Convert on import, export to other formats | New (s): through Collabora's conversion API |
| Text recognition (OCR) of scans | New, optional (q): an OCR side service; off by default |

## 2. Viewing and preview

| Drive feature | Selfcloud |
|---|---|
| Full-screen preview of images, PDF, video, audio and text | Built (k2), over signed links (g); text is read on the content route |
| Preview of office documents | New (s): read-only Collabora view, or a converted PDF |
| Thumbnails | New (s): images in-process, documents through Collabora |
| Details pane (size, type, owner, location, dates, who has access) | Built (k2): size, type, dates, creator, owner and your access. Location is in the breadcrumbs. Built (k3): "Share" and "Who has access" open the share dialog |
| Activity pane per file | New (r): from the audit log, filtered by access |

## 3. Organisation

| Drive feature | Selfcloud |
|---|---|
| Nested folders | Have |
| Personal space ("My Drive") | Built (n1): "My files" per user, made with the user. Counts against the account quota; per-user quota is slice p |
| Team spaces ("Shared drives") | Have in effect: the account's top-level folders. Built (n1): role tiers on the root, and owners as managers. Settings built (n2) |
| Starred | New (p) |
| Recent | New (p): from views and edits |
| Home page (recent, starred, recently shared, comments waiting) | New (p): plain rules, no machine learning |
| Shortcuts (one item in several places) | New (p) |
| Folder colours, descriptions | New (p) |
| Labels (admin-defined metadata fields) | New (p): typed fields per account (text, number, date, user, choice) |
| Sorting, grid and list views, density | Built (k2): sorting by name, modified and size (within the items loaded so far), list and grid views. Density is not built |
| Workspaces (ad hoc groups of files) | Skip: stars and shortcuts cover the need |
| "Suggested" files chosen by machine learning | Skip: replaced by the rules-based Home page |

## 4. Sharing and permissions

| Drive feature | Selfcloud |
|---|---|
| Roles: viewer, commenter, editor, owner | Built (n1): viewer, commenter, contributor, editor, manager. Drive's "content manager" is `editor` in the API; owners of the account hold manager on team spaces |
| Share a single file, or any folder at any depth | Built (n1): permissions on any node, inherited downward, additive only |
| Share with a group | Built (n1): account groups, with members of any role; removing a member revokes at once |
| Link sharing: restricted, organisation, anyone with the link | New (o): organisation-wide and public links |
| People outside the organisation open "anyone" links without an account | New (o) |
| Access expiry for a person | Built (n1, n2): `expires_at` is filtered at every read (n1). The API sets it, with a maximum from `sharing.max_expiry` (n2). Built (k3): the share dialog sets an end date, capped by `sharing_max_expiry_days` from `GET /info`, and shows it |
| Stop viewers and commenters from downloading, copying or printing | Built (n2): downloads, signed links and copies are refused for viewers and commenters. It does not stop viewing: the content still shows in the browser, and a viewer can copy what it sees. This deters, it does not secure |
| "Editors can change permissions" switch | Built (n2): `editors_can_share`, off by default |
| Transfer ownership | Built (n2): within one account. Uses the `owner_id` column (n1) |
| Access requests, with approve and deny | Built (n2): the API. Built (k3): the request form on a not-found view, and the Requests page with a count, approve (with a role) and deny |
| Email when something is shared with you | Built (n2): in the seven languages, with a `notify` switch |
| Pending invitations list | Have: pending users. Built (n2): `subject_status` and `subject_name` in the permission listing, for managers and account keys. Built (k3): the share dialog shows an "Invited" badge |
| Sharing limited to trusted domains (admin) | New (u): per-account allowlist for public links and invitations |
| Extra beyond Drive: password and expiry on public links, upload-only drop folders | New (o): optional, off by default |

## 5. Team spaces (Drive's "shared drives")

| Drive feature | Selfcloud |
|---|---|
| Create, rename, hide and delete | Have: create, rename, delete. Built (n2): hide, per user |
| Roles: manager, content manager, contributor, commenter, viewer | Built (n1), with the API names of section 8 |
| Restrictions: members only, organisation only, managers-only folder sharing, no download for viewers | Built (n2): members only, managers-only sharing and no download for viewers. Organisation only: the `allow_links` setting is stored, and does nothing until slice o enforces it |
| Background image | Skip: cosmetic; a colour (p) covers it |
| Membership and item limits | New (u): configurable caps |

## 6. Versions and recovery

| Drive feature | Selfcloud |
|---|---|
| Trash, deleted after 30 days; restore; empty trash | Built (m): `trash.retention`, default 30 days |
| Delete permanently, skipping the trash | Built (m): needs manager, not an editor, and the API flag is `?permanent=true` |
| Admin restore of deleted content | Built (m) as a restore by the one who trashed the item or a manager; admins never reach contents, so an account key restores |
| Version history for every file | Built (m): blobs never change, so a version is an old blob kept |
| Keep forever, name a version, restore a version | Built (m) |
| Automatic pruning (100 versions or 30 days) | Built (m): `versions.max_count` and `versions.max_age`; kept versions count toward quota |
| Undo for move, rename and delete | Built (m): the trash and the restore calls. Built (k2): the undo toast (8 seconds), with `If-Match` on rename and move. There is no change log |

## 7. Search

| Drive feature | Selfcloud |
|---|---|
| Search by name | New (q) |
| Filters: type, owner, location, modified and created date, starred, shared with me, trashed | New (q): filter chips in the interface, a query parameter in the API |
| Search by label values | New (q) |
| Full-text search inside documents | New (q): text extracted through Collabora's converter and indexed with SQLite FTS5, or PostgreSQL full text later. Lifts a first-release non-goal |
| Search text recognised in scans | New, optional (q): needs the OCR service |
| "/" to focus search | Planned (k) |

## 8. Collaboration

| Drive feature | Selfcloud |
|---|---|
| Comments on a file, replies, resolve | New (r): file-level threads. Comments inside a document stay in Collabora |
| @mentions, assign a comment | New (r) |
| Activity per file | New (r) |
| Notifications (email and in-app) for shares, mentions, comments and requests | New (r) |
| Who viewed a file (admin-controlled) | New (r): off by default, an account setting |
| Approvals (lock, review, approve or reject) | New, later (t2): proposed after the rest |
| Suggest mode inside documents | Planned (l): Collabora's track changes |
| eSignature | Skip: not verified in Google's docs; better served by a dedicated product |

## 9. Offline, sync and devices

| Drive feature | Selfcloud |
|---|---|
| Desktop sync app (streaming or mirroring) | Skip as a native app for now. New (t): a WebDAV endpoint, so operating systems and rclone can mount Selfcloud. A dedicated client can follow |
| Offline access in the browser | New, later: a service worker can cache recent files; not before the interface is stable |
| Mobile apps | New (t): an installable web app (PWA), with share-to-Selfcloud on Android. Native apps are separate products |
| Phone photo backup | Skip: needs a native app |

## 10. Storage

| Drive feature | Selfcloud |
|---|---|
| Storage quota | Have (h): per account |
| Per-user quota inside an account | New (p) |
| Storage page with the largest files | New (p) |
| Over-quota behaviour | Have (h): reads and deletes work, writes are refused |
| Kept versions and trash count toward quota | Built (m) |

## 11. Web interface

| Drive feature | Selfcloud |
|---|---|
| Keyboard shortcuts and a shortcut list (ctrl+/) | Built in part (k2): arrows, Enter, Space, F2, Delete, Backspace, Escape and Ctrl+A in the file list. The shortcut list (ctrl+/) is not built |
| Multi-select, right-click menu, grid and list, density, dark mode | Built in part (k2): multi-select, grid and list, dark mode (k1). No right-click menu, no density |
| Upload progress panel | Built (k2): the upload tray, with pause, resume, cancel and retry |
| Download progress, download a selection as zip | New (s): streamed zip, no temporary copy |
| Copy link, open in new tab | Planned (k), not in k2 |
| Cut, paste, paste as shortcut | New (p/k) |

## 12. Admin and security

| Drive feature | Selfcloud |
|---|---|
| Audit log | Have (b). New (u): a viewer in the interface and CSV export |
| External sharing controls, trusted domains | New (u) |
| Retention rules and legal hold | New (u): per account; a hold blocks deletion and purging |
| Malware scanning | New (u): optional ClamAV (clamd) check on uploads |
| Data loss prevention | Skip for now: needs content classification. Revisit after full-text search |
| Client-side encryption | Skip: conflicts with server previews, search and office editing; full encryption is a first-release non-goal |
| Data regions | Skip: one installation is one location; run another installation for another region |
| Spam and phishing warnings on shared files | Skip for now |

## 13. API and integrations

| Drive feature | Selfcloud |
|---|---|
| Files, permissions, revisions, comments and team-space APIs | Have for files (e). Permissions and team spaces (n1, n2) and revisions and trash (m) are built. Comments are New (r) |
| Changes feed (what changed since a token) | New (t): for sync clients and API clients |
| Push notifications | Planned (j): webhooks |
| Export to other formats | New (s) |
| Thumbnails | New (s) |
| "Open with" office editor | Planned (l) |
| Embeddable file picker for client applications | New (t): a small script client apps embed, over delegated tokens (i) |

## Proposed new slices

All of them come after slice i, so API clients can still switch over after i as planned. To avoid
building the web interface twice, slices n and m (the new sharing model and trash/versions) should
come **before** k.

| Slice | Contents | Size |
|---|---|---|
| n1 | Sharing model (built, see the sections marked (n1) above): roles, permissions on any node with inheritance, groups, personal "My files", team-space owners, the grants routes on permissions, `GET /shared`, token node scope | Large |
| n2 | Sharing v2 (built, see the rows marked Built (n2) above): access expiry, no-download, "editors can share", ownership transfer, team-space restrictions and hiding, access requests, share emails | Medium |
| m | Trash, version history, keep forever, restore, undo | Medium |
| k | Web interface (as planned, on the new model). Complete after k4. k3 (built): the sharing interface: share dialog, Shared with me, access requests, hidden spaces, space settings, and the account directory search (`GET /directory`). k4 (built): owner pages (overview, people, groups, team spaces, audit log) and installation admin pages (tenants, tenant tabs, admins, installation facts), with the owner guard and list filters in the API. Known gaps: design section 10 | Large |
| o | Organisation and public links, link expiry and password, drop folders, admin policy | Medium |
| p | Starred, recent, Home, shortcuts, colours, descriptions, labels, per-user quota, storage page | Medium |
| q | Search: filters and chips, label search, full text (FTS5), optional OCR | Large |
| r | Comments, mentions, activity pane, notifications, who-viewed | Medium |
| s | Thumbnails, document previews, conversions and export, zip download | Medium |
| l | WOPI host for Collabora (as planned) | Medium |
| j | Webhooks (as planned) | Small |
| t | Changes feed, file picker, WebDAV, installable web app | Medium |
| u | Admin: audit viewer and export, sharing policies, retention and hold, ClamAV, limits | Medium |
| t2 | Approvals | Small |

Roughly, this doubles the remaining work. Slices n and q are the largest. The PostgreSQL option
(decision 7) becomes more useful with full-text search and should land before q.
