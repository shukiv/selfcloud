# Feature parity with Google Drive

Status: **approved 2026-10-28** (design.md §15, decisions 11 to 13). Search limits: full text up to 50 MiB, OCR up to 10 MiB images and 20 MiB PDFs.

The goal is to offer what Google Drive offers, matched in function, not in look: the screens
follow Selfcloud's own design system. The inventory behind this file covers 213 Drive features
from Google's help centre, admin help and Drive API v3 reference.

Each feature is marked:

- **Have**: built in slices a to i.
- **Built (x)** and **Built in part (x)**: built in slice x. A part that is not built is named in the row.
- **Planned (x)**: in slice x, not built yet.
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
| Maximum file size (5 TB in Drive) | Have: settable, default 100 GiB (`upload.max_file_size`) |
| Daily upload cap per user | New (u): `upload.daily_cap_per_user` |
| Copy-size limit, folder depth limit, items per folder | Built in part (copy limit): `copy.max_nodes`, 50,000 by default; a copy above 10,000 nodes runs as a chunked job (decision 34). New (u): depth and per-folder caps |
| Convert on import, export to other formats | Export built (s3, decision 37; the web interface's Export menu, s4b, decision 59): a fixed list of formats through Collabora's conversion API, run live against Collabora Online on 2026-10-31. Convert on import: New (s) |
| Text recognition (OCR) of scans | New, optional, a later slice after q (decision 40): an OCR side service; off by default |

## 2. Viewing and preview

| Drive feature | Selfcloud |
|---|---|
| Full-screen preview of images, PDF, video, audio and text | Built (k2), over signed links (g); text is read on the content route. Video and audio play to the end of a long file: a new link is asked for when the first one ends, and a format the browser cannot play is said so (s4c, decision 42). Double click, a tap on a phone, and a play mark on media rows. Pictures open in a lightbox over the dimmed page (decision 60). A Play media button plays a folder's audio and video (decision 61). |
| Video and audio player: own controls (play, seek with the buffered part, time, volume kept between files, mute, speed, full screen, picture in picture) and the keys | Built (x1, decision 45): antd controls over the element, with no player library; the browser's own controls are not used. Mini player and Media Session (x2), subtitles (x2) are planned |
| Playlist from a folder or a selection: play selected, next at the end, shuffle, repeat one, repeat all, add next, add to end, remove, reorder by drag and by keys | Built (x1, decision 45): the folder is read a page at a time, the queue holds at most 500 files, a file that cannot play is skipped, and playback stops when every file in a row fails. Saved playlists are built (x4, decision 58). In the viewer the playlist is a drawer that opens from a Playlist button in the head row, beside Download; on a wide window the drawer takes its width beside the video (x6, decision 63) |
| Resume playback where it stopped | Planned (x2): a small table on the server, per user and per file |
| Cover art and tags of audio files | Built (x3, decision 56): title, artist, album and duration are read on the server from MP3, FLAC, MP4 and M4A, Ogg Vorbis and Ogg Opus files; the cover is the file's thumbnail. An audio file's stage shows its cover large, and a file without a cover of its own takes the folder's picture by the rule of decision 63 (x6: a listing of up to 250 entries, kept for 5 minutes; a share visitor gets the icon only). Transcoding (x5) is not built |
| Preview of office documents | Built (s3, decision 37; s4b, decision 59): while `office.enabled` is on, the viewer's Preview button has Collabora convert the document to PDF and shows it; run live against Collabora Online on 2026-10-31. The editor's view mode is in the API and, from l4 (decision 73), in the interface as Open in viewer |
| Thumbnails | Built in part (s1, s4a): JPEG, PNG and GIF thumbnails from the API, made in a child process, and shown in the grid's tiles (s4a, decision 38; the list view has no thumbnails). Document previews are built (s3, decision 37; s4b, decision 59) |
| Details pane (size, type, owner, location, dates, who has access) | Built (k2): size, type, dates, creator, owner and your access. Location is in the breadcrumbs. Built (k3): "Share" and "Who has access" open the share dialog |
| Activity pane per file | New (r): from the audit log, filtered by access |

## 3. Organisation

| Drive feature | Selfcloud |
|---|---|
| Nested folders | Have |
| Personal space ("My Drive") | Built (n1): "My files" per user, made with the user. Counts against the account quota and the user's own limit (built, p1); team spaces count against the account only |
| Team spaces ("Shared drives") | Have in effect: the account's top-level folders. Built (n1): role tiers on the root, and owners as managers. Settings built (n2) |
| Starred | Built (p2): a star per user on any node it reaches, at most 5,000; the star shows on listings and on the Starred page |
| Recent | Built (p2): from the user's own views (signed-in users, GET only) and edits, kept 30 days and 500 per user, clearable |
| Home page (recent, starred, recently shared, comments waiting) | Replaced (decision 53): there is no Home page. A person lands on My files. Recent, Starred and Shared with me are their own pages, the access requests waiting show with their count in the sidebar, and a storage notice at 80% shows on My files. Comments wait for slice r |
| Shortcuts (one item in several places) | Built (p3): a shortcut to a file or directory of the same account, made from the Add shortcut dialog or the API |
| Folder colours, descriptions | Built (p3): twelve palette colours on folders; a description of up to 1,000 characters on any item |
| Labels (admin-defined metadata fields) | Built (p4): typed fields per account (text, number, date, user, choice), with restricted fields. Search by label is New (q) |
| Sorting, grid and list views, density | Built (k2): sorting by name, modified and size, list and grid views; the sort covers the whole folder, done by the server (slice sort, decision 47), for the web listing and for the API's `sort` and `order` on the children routes. Density is not built |
| Workspaces (ad hoc groups of files) | Skip: stars and shortcuts cover the need |
| "Suggested" files chosen by machine learning | Skip: replaced by rules, not machine learning: the Recent, Starred and Shared with me pages, and the sidebar's requests count (decision 53) |

## 4. Sharing and permissions

| Drive feature | Selfcloud |
|---|---|
| Roles: viewer, commenter, editor, owner | Built (n1): viewer, commenter, contributor, editor, manager. Drive's "content manager" is `editor` in the API; owners of the account hold manager on team spaces |
| Share a single file, or any folder at any depth | Built (n1): permissions on any node, inherited downward, additive only |
| Share with a group | Built (n1): account groups, with members of any role; removing a member revokes at once |
| Link sharing: restricted, organisation, anyone with the link | Restricted, organisation and anyone-with-the-link links built (o1, o2): an organisation link opens for members of the account; an anyone link opens for whoever holds it, viewer only, with an end date and an optional password. Manage > Links lists and deletes them (o4), and the file list shows a link badge |
| People outside the organisation open "anyone" links without an account | Built (o2): the link's secret opens it for anyone, with a password when set. Visitors use a 15-minute credential, and are kept to the link's subtree |
| Access expiry for a person | Built (n1, n2): `expires_at` is filtered at every read (n1). The API sets it, with a maximum from `sharing.max_expiry` (n2). Built (k3): the share dialog sets an end date, capped by `sharing_max_expiry_days` from `GET /info`, and shows it |
| Stop viewers and commenters from downloading, copying or printing | Built (n2): downloads, signed links and copies are refused for viewers and commenters. It does not stop viewing: the content still shows in the browser, and a viewer can copy what it sees. This deters, it does not secure |
| "Editors can change permissions" switch | Built (n2): `editors_can_share`, off by default |
| Transfer ownership | Built (n2): within one account. Uses the `owner_id` column (n1) |
| Access requests, with approve and deny | Built (n2): the API. Built (k3): the request form on a not-found view, and the Requests page with a count, approve (with a role) and deny |
| Email when something is shared with you | Built (n2): in the seven languages, with a `notify` switch |
| Pending invitations list | Have: pending users. Built (n2): `subject_status` and `subject_name` in the permission listing, for managers and account keys. Built (k3): the share dialog shows an "Invited" badge |
| Sharing limited to trusted domains (admin) | New (u): per-account allowlist for public links and invitations |
| Extra beyond Drive: password and expiry on public links, upload-only drop folders | Expiry built in o1 for organisation links, optional. Anyone links: expiry and password built in o2. Drop folders ("Request files") built in o3: upload-only, with an end date, an optional password, a title and message, and an optional name and email. Off by default. Built in o4: an owner's list of every link in the account, with "Delete all public links"; ended links are swept a day after they end |

## 5. Team spaces (Drive's "shared drives")

| Drive feature | Selfcloud |
|---|---|
| Create, rename, hide and delete | Have: create, rename, delete. Built (n2): hide, per user |
| Roles: manager, content manager, contributor, commenter, viewer | Built (n1), with the API names of section 8 |
| Restrictions: members only, organisation only, managers-only folder sharing, no download for viewers | Built (n2): members only, managers-only sharing and no download for viewers. Organisation only: the `allow_links` setting is live (o1), and pauses the space's links when it is off |
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
| Search by name | Built (q1, q2, decision 40): a query parameter in the API, and the search page of the web interface (q4, decision 54) |
| Filters: type, owner, location, modified and created date, starred, shared with me, trashed | Partly built (q2, decision 40): kind, extension, modified and size, scope, label values. Owner, location and the others are not in v1. The page (q4, decision 54) has chips for type, extension, modified, size and label values; a scope is in the API but not yet in the page |
| Search by label values | Built (q2, decision 40): typed predicates in the API (text prefix, number and date ranges, user and choice); the page (q4, decision 54) offers text, number, date and choice values, not user values |
| Full-text search inside documents | Built (q3, decision 46): plain text read in process, and documents converted to text through Collabora (`txt`, with `office.enabled`); indexed with SQLite FTS5, words only, with a snippet per hit. Spreadsheets, presentations and PDFs are not searchable through `txt` (refused by the converter, decision 46). PostgreSQL full text later. Lifts a first-release non-goal. The page shows the matches with their snippets (q4, decision 54) |
| Search text recognised in scans | Planned, optional (a later slice after q, decision 40): needs an OCR service that is not in the image |
| "/" to focus search | Planned (k) |

## 8. Collaboration

| Drive feature | Selfcloud |
|---|---|
| Comments on a file, replies, resolve | Built (r1): file and folder threads, one reply level, edit, delete and resolve. Plain text, 2,000 characters and 50 lines at most. Comments inside a document stay in Collabora. The account can turn comments off. The web interface is built (r7a, decision 55): the comments panel, replies, edit, delete, resolve, and the owner's switch. Comment search and assignment are not built |
| @mentions, assign a comment | Mentions built (r2): a person named in a comment must reach the node; a people list suggests them. A mention and a reply notify the person in the app and by email, once an hour at most (r3, decision 43). Assignment is deferred (decision 39) |
| Activity per file | Built (r4, r7a, decision 55): the file's Activity tab, read from the audit log, with the role and reach filters. The web tab shows the sentences of the audit page |
| Notifications (email and in-app) for shares, mentions, comments and requests | Partly built (r3, decision 43): in-app and email for mentions and replies, with an email switch per user and a one-click unsubscribe. Shares and requests in the app are not built; the bell, the notifications page and the notification email settings are built (r7b, decision 57); shares and requests in the app are still not built; share mail is unchanged |
| Who viewed a file (admin-controlled) | Built (r5, r7a, decisions 51 and 55): off by default, the owner's switch, managers of the file only, with the note that it is a convenience and not evidence. Not built: the installation admin's allowance per tenant (question 12), and a notice on the file for people who cannot read the switch |
| Approvals (lock, review, approve or reject) | New, later (t2): proposed after the rest |
| Suggest mode inside documents | Planned (l): Collabora's track changes |
| eSignature | Skip: not verified in Google's docs; better served by a dedicated product |

## 9. Offline, sync and devices

| Drive feature | Selfcloud |
|---|---|
| Desktop sync app (streaming or mirroring) | Skip as a native app for now. New (t): a WebDAV endpoint, so operating systems and rclone can mount Selfcloud. A dedicated client can follow |
| Offline access in the browser | New, later: a service worker can cache recent files; not before the interface is stable |
| Mobile apps | New (t): an installable web app (PWA). Part 1 (install, fast start, offline page, update offer) is decision 65; part 2, share-to-Selfcloud on Android and desktop Chrome, is slice pwa2. Native apps are separate products |
| Phone photo backup | Skip: needs a native app |

## 10. Storage

| Drive feature | Selfcloud |
|---|---|
| Storage quota | Have (h): per account |
| Per-user quota inside an account | Built (p1): each user's limit, with an account default; no default out of the box |
| Storage page with the largest files | Built (p1): a storage page for every user; Manage > Storage for owners |
| Storage use in the sidebar | Built (d2, decision 66): the sidebar's Storage entry shows the share of the person's own limit ("Storage (61% full)"), a bar (warning colour from 80%, error colour at 100%) and "1.2 GB of 2 GB used"; with no limit, the bytes used and no bar. It counts what the person's limit applies to (My files; team spaces count against the account), and is read again after uploads, deletes and the trash |
| Over-quota behaviour | Have (h): reads and deletes work, writes are refused |
| Kept versions and trash count toward quota | Built (m) |

## 11. Web interface

| Drive feature | Selfcloud |
|---|---|
| Keyboard shortcuts and a shortcut list (ctrl+/) | Built in part (k2, w1): arrows, Enter, Space, F2, Delete, Escape and Ctrl+A in the file list; Backspace and Alt+Up go up a folder, Ctrl+C, Ctrl+X and Ctrl+V copy, cut and paste within the tab (w1, decision 44). The shortcut list (ctrl+/) is not built |
| Multi-select, right-click menu, grid and list, density, dark mode | Built in part (k2, w1): multi-select, grid and list, dark mode (k1). The right-click menu, with a long press on a finger, is built (w1, decision 44), on a row, a selection and empty space. Density is not built. The selection actions are in a toolbar above the listing, not in a bar over the rows (w1) |
| Folder tree in the sidebar; tiles with folders in their own band | Built (d1, decision 62): the folder tree is in the sidebar under My files, with the team spaces; a phone shows it in the drawer. The listing takes the width beside the sidebar. In the grid, folders are one-line chips in a band above the files, and each file is a card of one height with its name on one line, and a star and a menu on every item. Changed in d3 (decision 69): the tree starts folded; a folder opens only on a click of its arrow or on Right arrow (a click on its name opens the folder in the listing and does not open it in the tree); the current place is a bar at the row's start edge with the mark colour, and no background (option B); the brand name is no longer drawn in the sidebar or the top bar, and it stays in the browser tab and the sign-in pages. Not built: a filter row, the duplicate title, and a drag that scrolls the sidebar's tree at its edge |
| A New button in the sidebar | Built (d2, decision 66): a large + New at the top of the sidebar, in the rows' icon and name columns, with New folder, Upload files and Upload folder. It acts on the folder shown, else on My files; a folder the person cannot add to shows the entries disabled, with the reason. The toolbar's New is hidden where the sidebar is shown and kept on a phone. New documents come with office editing. Changed in d3 (decision 69): the button is as wide as its text, at the sidebar's start edge, and its top lines up with the search box |
| Upload progress panel | Built (k2): the upload tray, with pause, resume, cancel and retry. It folds itself when the uploads end, and an error keeps it open; the listing keeps its last rows clear of it (w1, decision 44) |
| Download progress, download a selection as zip | Built (s2, decision 35; the interface in s4a, decision 38): a stored zip made as a job, served with `Range`, kept for `zip.ttl` and only for its maker. The interface offers Download as zip for a folder and a selection, shows the job's progress as a spinner (the job has no percentage), and names the items left out. The streamed zip with no temporary copy (row 160's alternative) is not built |
| Copy link, open in new tab | Planned (k), not in k2 |
| Cut, paste, paste as shortcut | Built in part (w1, decision 44): cut, copy and paste with Ctrl+X, Ctrl+C and Ctrl+V within the tab, over the existing move and copy routes and their undo; a cut item is dimmed until it is pasted or Escape cancels it. The system clipboard is not used. Paste as shortcut is not built |
| Icon buttons with tooltips; file icons by type | Built (ic1, decision 68; ic2, decision 72): the files area is icons first, with a tooltip and a name on each: the player and viewer heads, the player's bar and mini player, the toolbar (New is a plus icon under 480 px), the selection bar as a borderless pill, the list's More button and the tiles' menu, and file and folder icons coloured by type. The top bar's language, theme and account are icons too. The other pages (Details, the trash, the shared page, sign-in, Manage, Admin, the playlist drawer) are icons first too (decision 72) |
| List row end: More on every row, and Share, Download, Rename and Star icons on a hovered or selected row; checkboxes that take the file icon's place on hover, and on every row while something is selected | Built (w3, decision 70): with a mouse only; a finger keeps the checkbox column and no icons. The icons are the menu's own actions, with the same permissions. Selected rows have an accent band and edge, hovered rows a grey band |
| Right-click and More menu grouped like Drive: Open or Preview, Download, Rename | Built (w3, decision 70): the first group flat; Share, Organize (Move to, Copy to, Add shortcut, Cut, Copy, Paste, Star) and Information (Details, Versions) as submenus; Move to trash last. Shortcuts at the right: Ctrl on Windows and Linux, the command sign on a Mac. Star is a menu entry and a filled star after a starred name; the outline star button is gone |

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
| Files, permissions, revisions, comments and team-space APIs | Have for files (e). Permissions and team spaces (n1, n2) and revisions and trash (m) are built. Comments are built (r1) |
| Changes feed (what changed since a token) | New (t): for sync clients and API clients |
| Push notifications | Built (j1 to j4): webhook endpoints, events, the delivery worker and quota warnings (decisions 26 to 30) |
| Export to other formats | Built (s3, decision 37): `POST /nodes/{nod}/exports`, through Collabora's conversion; run live against Collabora Online on 2026-10-31 |
| Thumbnails | Built in part (s1, s4a): images from the API; the grid's tiles show them (s4a, decision 38) |
| "Open with" office editor | Built (l1 to l4, decision 73): the editor in the interface, Open in editor (a file the caller may edit) and Open in viewer (read only), from the row menu and the viewer; the WOPI host; rename from the editor (RenameFile); the lock mark, Details and a manager's confirmed end of an editing session. Checked against a fake Collabora only; a live run is not done |
| Embeddable file picker for client applications | New (t): a small script client apps embed, over delegated tokens (i) |

## Proposed new slices

All of them come after slice i, so API clients can still switch over after i as planned. To avoid
building the web interface twice, slices n and m (the new sharing model and trash/versions) should
come **before** k.

| Slice | Contents | Size |
|---|---|---|
| n1 | Sharing model (built, see the sections marked (n1) above): roles, permissions on any node with inheritance, groups, personal "My files", team-space owners, the grants routes on permissions, `GET /shared`, token node scope | Large |
| n2 | Sharing v2 (built, see the rows marked Built (n2) above): access expiry, no-download, "editors can share", ownership transfer, team-space restrictions and hiding, access requests, share emails | Medium |
| m | Trash, version history, keep forever, restore, undo (built) | Medium |
| k | Web interface (as planned, on the new model). Built in four parts. k1 (built): the foundation and the sign-in, verification and password pages. k2 (built): the file browser, uploads, previews, Trash and versions. k3 (built): the sharing interface: share dialog, Shared with me, access requests, hidden spaces, space settings, and the account directory search (`GET /directory`). k4 (built): owner pages (overview, people, groups, team spaces, audit log) and installation admin pages (tenants, tenant tabs, admins, installation facts), with the owner guard and list filters in the API. Known gaps: design section 10 | Large |
| c2 | Sign-in links for trusted clients (built, after k) | Small |
| adm1 | Installation admin access to tenant users: a reset link, a typed password (the user changes it at sign-in) and sign-in as the user for an hour, off unless `admin.tenant_access` is on (decision 41) | Small |
| i2 | Delegated tokens: per-user revoke and the all-reachable scope (built, after k) | Small |
| o | Organisation and public links, link expiry and password, drop folders, admin policy. Built in four parts: o1 to o4 | Medium |
| p | Starred, recent, Home, shortcuts, colours, descriptions, labels, per-user quota, storage page (design decision 24). In five parts: p1 per-user quota and storage pages, p2 starred, recent and Home, p3 shortcuts, colours and descriptions, p4 labels (all built); p5 the OpenAPI file, the design record, these rows, the smoke test and the security review (decision 36) | Medium |
| pg | PostgreSQL as an option for the metadata store (design decision 7), after q (decision 40) | Medium |
| q | Search by name with filters and label values (built in parts q1 and q2, decision 40); content text (built as q3, decision 46); the web page (q4, decision 54, built) and optional OCR (a later slice) | Large |
| r | Comments, mentions, notifications, the activity pane and who-viewed built (r1 to r5, r7a, r7b, decisions 39, 43, 50, 51, 55 and 57); the comment webhook (r6) planned; the bell, notifications page and notification settings built (r7b, decision 57) | Medium |
| s | Thumbnails (built in part, s1 and s4a: images from the API and the grid's tiles), document previews, conversions and export, zip download (built, s2 and s4a) | Medium |
| l | WOPI host for Collabora (built in parts l1 to l3; the interface button, Rename (l4) and the README example are not built) | Medium |
| j | Webhooks (built, j1 to j4) | Small |
| t | Changes feed, file picker, WebDAV, installable web app | Medium |
| u | Admin: audit viewer and export, sharing policies, retention and hold, ClamAV, limits | Medium |
| t2 | Approvals | Small |
| sort | Folder listings sorted by the server, so a long folder sorted by size or date is right from the first page (decision 47) | Small |
| audit | Audit log kept for a set period (`audit.retention_days`, 365 days by default, `0` keeps for ever), deleted by the sweep; an account's entries go with the account (decision 48). Retention is a deletion, so an operator with legal duties sets the period | Small |
| w | A normal file manager, approved 2026-10-31 (`design.md` section 14). w1: a toolbar always on top, a right-click menu, Ctrl+C, Ctrl+X and Ctrl+V. w2: drag to move, a folder tree, rubber-band selection | Medium |
| x | A built-in video and audio player, approved 2026-10-31 (`design.md` section 14). x1: own controls, playlist, queue, shuffle, repeat, auto-next. x2: audio while browsing, resume, subtitles. x3: cover art and tags. x4: saved playlists. x6: the video fills the viewer, the playlist is a drawer, and audio shows a large cover (decision 63). Server conversion of video is deferred by the owner | Medium |

Roughly, this doubles the remaining work. Slices n and q are the largest. The PostgreSQL option
(decision 7) was first scheduled before q. The owner decided on 2026-10-31 that it comes after q (decision 40).
