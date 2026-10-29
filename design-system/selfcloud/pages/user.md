# Drive pages (every signed-in user)

Overrides `MASTER.md` for the drive itself: what a member sees, and what owners see under
Drive. A user reaches only the top-level folders shared with them; inside a folder they can do
what their level allows. API: `/api/v1/folders` (folders the caller can reach), file routes
(slice e), tus uploads (slice f), downloads (slice g), `/api/v1/me`, `/me/password`.
The folder browser, uploads, previews and the trash are slice k2; the sharing screens (the share
dialog, Shared with me, access requests, hidden spaces and space settings) are k3.

## Shared with me (`/shared`, slice k3)

`/` is My files, as it was in k2. The shared list is its own page.

- Title "Shared with me". A list of the items other people shared with the caller (`GET /shared`), one
  row each: the name (a link to `/n/{nod}`), the role as a badge ("Can view", "Can edit", ...), and the
  last change. A link opens the item in its folder, through the file browser.
- The owner's name appears only when it is the caller. The API gives no other name for an owner.
- Empty: "Nothing is shared with you yet. Ask an owner of your organisation to share a folder."
- Loads more with a "Load more" button; no infinite scroll here.

## Folder browser (`/files/{fld}`, `/files/{fld}/{dir}`, `/n/{nod}`)

Built in slice k2. `/files` is My files; a team space is `/files/{fld}`; a directory is
`/files/{fld}/{dir}`. `/n/{nod}` (the link in share mails) opens a file's preview in its folder,
or a directory's parent with the directory selected. Anything the caller cannot reach is the
not-found view.

- **Breadcrumbs:** Team A › Finance › 2026 › Q3. Only ancestors the caller can reach are shown;
  the walk stops at the first one it cannot reach. A long path collapses its middle into a "…"
  menu. Chevrons mirror in right-to-left.
- **Toolbar:** New folder, Upload files, Upload folder (when the caller may add here), then the
  list or tiles switch and, for tiles, the sort. Buttons the role does not allow are not shown.
  A muted line for view-only places is not built in k2.
- **List view (default):** a checkbox column, type icon, Name (`<bdi>`), Size, Modified and "Your
  access". Folders first, then files; Name, Size and Modified are sortable with `aria-sort`. The
  list is a grid with one roving tab stop. Sorting applies to the items loaded so far; the page
  says so while more pages remain.
- **Selection:** click selects; Ctrl or Cmd adds or removes; Shift selects a range; Ctrl or Cmd
  with A selects all. With a selection a bar sits above the list: "3 selected", then the actions
  its roles allow: Download (one file, unless no-download applies), Preview, Details, Versions,
  Rename (one item), Move to…, Copy to…, Delete, Clear selection.
- **Keyboard:** arrows move (left and right in tiles, mirrored in right-to-left), Shift extends,
  Home and End jump, Enter opens, Space toggles, F2 renames, Delete asks to confirm and moves to
  the trash, Backspace or Alt+↑ goes up, Escape clears the selection.
- **Drag and drop:** files dropped on the list upload into the folder or directory on screen,
  and a drop zone shows while dragging. Dropping onto a folder row, and dragging to move, are
  not built; Move to… covers moving.
- **Move to / Copy to** dialog: a list of spaces, then folders and directories one level at a
  time, with Back. A place the caller only views shows "Can view only" and cannot be chosen.
- **Rename** is inline in the row; Enter saves, Escape cancels, and a name problem or a name
  already taken shows under the field.
- **Delete** asks to confirm and says the item goes to the trash. An undo toast for 8 seconds
  brings it back. Deleting for good is in the trash page only.
- **Undo toast:** after delete, move, rename or a restore, for 8 seconds, announced politely.
  A changed item (`412`) reads "it changed since; undo not possible".

## Upload tray

- Shown at the bottom inline-end corner while there are uploads; its header carries the summary
  ("2 uploads running, 42%") and a Hide or Show button. Clear finished empties it.
- Per file: name, state, a progress bar, the bytes sent of the total, the speed while it runs,
  and the controls the state allows: Pause, Resume, Retry, Cancel, Dismiss. Time left is not
  shown in k2.
- States: Waiting in line, Uploading, Paused, Waiting to retry, Name taken, Choose another name,
  Interrupted, Done, Skipped, Failed (with the reason), Cancelled.
- A name taken asks once per batch: replace, keep both, or skip, with "do this for the rest"
  (`conflict`). A name taken when the last piece arrives asks for a new name (`awaiting_name`).
- Uploads go in pieces of the server's chunk size; a dropped connection retries with backoff and
  resumes from the server's offset. Three uploads run at once; the rest wait in line.
- After a reload the tray lists unfinished uploads as Interrupted, and "Choose the file to
  resume" asks for the same file again (browsers cannot reopen a file by themselves). A different
  file is refused with a message.
- Leaving the page while uploads run asks for confirmation (`beforeunload`).
- Quota and storage refusals show the space left and the space needed. `too_many_uploads` waits and
  tries again. Screen readers hear each completion and each failure.

## Preview (`/files/{fld}/{dir}?preview={nod}`)

- A full-screen overlay: images, PDFs, video and audio load through a short signed link; text
  (the first 1 MB) is shown as text in a `pre`; Markdown is shown as text too. Anything else
  shows a card with the file's kind and a Download button when downloads are allowed.
- Previous and next move with the arrow keys (mirrored in right-to-left) or the buttons; Escape
  closes, and focus returns to the element that opened it.
- Downloads are offered only where no-download does not apply. A file served from the content
  origin (`content_url`) loads there; the UI never renders user HTML or SVG inline (an SVG
  image loads as an `img`).

## Account (`/account`)

- Name, email (read-only), Language (each in its own name), Theme (System, Light, Dark).
- Change password: current, new, confirm. Rate-limit errors are shown as text with the wait.
- Sign out, and "Sign out everywhere".

## Share dialog (slice k3)

Opened from the selection bar ("Share") and the details panel ("Share" where the caller may share,
and "Who has access"). Focus is trapped; Escape closes it and returns focus to the opener.

- **Your access.** A person below manager reads "You have <role> access" and sees only its own rows,
  as the server answers. A manager sees everyone who holds a role on the item, and above it.
- **Each row.** The name ("You" for the caller), a "Group" badge for a group, an "Invited" badge for a
  pending user, "from <name>" (or "from a parent folder" when the ancestor is not readable), and
  "Ends <date>" when the access ends. A manager changes the role from a dropdown and removes a row
  with a confirmation. An inherited row offers "Open the parent" instead of removal.
- **Add people.** A combobox over `GET /directory`, with the arrow keys, Enter and Escape. The roles
  offered stop at the caller's cap. An end date is optional and capped at the server's limit. The
  message is at most 500 characters. Notify is on by default. A reply that says no mail went out gives
  the reason in a sentence, and the share stands.
- **Errors** are short sentences by code: forbidden, members only, unknown person, the end date too far
  ahead (with the limit in days), invalid details, rate limited.
- **Advanced (managers).** "Editors can share", "Viewers and commenters can't download" (a switch that
  says it discourages, not prevents, downloads), and transfer ownership for the owner, with a
  confirmation that says where the item goes.
- **Access requests.** A not-found view on an item offers "Request access": a role and an optional message.
  The confirmation is one sentence whatever the server answered, "If this item exists, the people who
  manage it have been asked."
- **Requests** (`/requests`): requester, item name when visible, role asked for, and the message. Approve
  with a role no higher than the caller's, or deny.
- **Team spaces** in the sidebar: a "…" menu with Hide or Show, and Space settings for a manager of the
  space's root. "Show hidden spaces" lists hidden ones, marked "Hidden".
