# Feature parity with Google Drive

Status: **proposed 2026-10-28, awaiting approval.** Nothing here is built until it is approved.

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
| Upload files and folders, with drag and drop | Have: resumable uploads (f); folder upload comes with the web interface (k) |
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
| Full-screen preview of images, PDF, video, audio and text | Planned (k), over signed links (g) |
| Preview of office documents | New (s): read-only Collabora view, or a converted PDF |
| Thumbnails | New (s): images in-process, documents through Collabora |
| Details pane (size, type, owner, location, dates, who has access) | New (k): the data already exists |
| Activity pane per file | New (r): from the audit log, filtered by access |

## 3. Organisation

| Drive feature | Selfcloud |
|---|---|
| Nested folders | Have |
| Personal space ("My Drive") | New (n): "My files" per user, under the account quota |
| Team spaces ("Shared drives") | Have in effect: the account's top-level folders. New (n): Drive's role tiers and settings for them |
| Starred | New (p) |
| Recent | New (p): from views and edits |
| Home page (recent, starred, recently shared, comments waiting) | New (p): plain rules, no machine learning |
| Shortcuts (one item in several places) | New (p) |
| Folder colours, descriptions | New (p) |
| Labels (admin-defined metadata fields) | New (p): typed fields per account (text, number, date, user, choice) |
| Sorting, grid and list views, density | Planned (k) |
| Workspaces (ad hoc groups of files) | Skip: stars and shortcuts cover the need |
| "Suggested" files chosen by machine learning | Skip: replaced by the rules-based Home page |

## 4. Sharing and permissions

| Drive feature | Selfcloud |
|---|---|
| Roles: viewer, commenter, editor, owner | New (n): viewer, commenter, editor, plus manager on team spaces |
| Share a single file, or any folder at any depth | New (n): permissions on any node, inherited downward |
| Share with a group | New (n): account groups |
| Link sharing: restricted, organisation, anyone with the link | New (o): organisation-wide and public links |
| People outside the organisation open "anyone" links without an account | New (o) |
| Access expiry for a person | New (n): up to a configurable maximum |
| Stop viewers and commenters from downloading, copying or printing | New (n): enforced on download and signed-link routes. In-browser viewing still shows the content; this deters, it does not secure |
| "Editors can change permissions" switch | New (n) |
| Transfer ownership | New (n): within one account |
| Access requests, with approve and deny | New (n) |
| Email when something is shared with you | New (n): in the seven languages |
| Pending invitations list | Have: pending users. New (n): shown on the share dialog |
| Sharing limited to trusted domains (admin) | New (u): per-account allowlist for public links and invitations |
| Extra beyond Drive: password and expiry on public links, upload-only drop folders | New (o): optional, off by default |

## 5. Team spaces (Drive's "shared drives")

| Drive feature | Selfcloud |
|---|---|
| Create, rename, hide and delete | Have: create, rename, delete. New (n): hide |
| Roles: manager, content manager, contributor, commenter, viewer | New (n) |
| Restrictions: members only, organisation only, managers-only folder sharing, no download for viewers | New (n) |
| Background image | Skip: cosmetic; a colour (p) covers it |
| Membership and item limits | New (u): configurable caps |

## 6. Versions and recovery

| Drive feature | Selfcloud |
|---|---|
| Trash, deleted after 30 days; restore; empty trash | New (m): `trash.retention`, default 30 days |
| Delete permanently, skipping the trash | New (m): editors with the right setting; API flag |
| Admin restore of deleted content | New (m): admins and owners restore within the window |
| Version history for every file | New (m): blobs never change, so a version is an old blob kept |
| Keep forever, name a version, restore a version | New (m) |
| Automatic pruning (100 versions or 30 days) | New (m): settable; kept versions count toward quota |
| Undo for move, rename and delete | New (m/k): undo toast backed by the trash and the change log |

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
| Kept versions and trash count toward quota | New (m) |

## 11. Web interface

| Drive feature | Selfcloud |
|---|---|
| Keyboard shortcuts and a shortcut list (ctrl+/) | Planned (k): the same keys where browsers allow, listed in the interface |
| Multi-select, right-click menu, grid and list, density, dark mode | Planned (k) |
| Upload progress panel | Planned (k) |
| Download progress, download a selection as zip | New (s): streamed zip, no temporary copy |
| Copy link, open in new tab | Planned (k) |
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
| Files, permissions, revisions, comments and team-space APIs | Have for files (e). New (m, n, r) for the rest |
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
| n | Sharing v2: roles, permissions on any node with inheritance, groups, personal "My files", team-space roles and restrictions, expiry, no-download, ownership transfer, access requests, share emails | Large |
| m | Trash, version history, keep forever, restore, undo | Medium |
| k | Web interface (as planned, on the new model) | Large |
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
