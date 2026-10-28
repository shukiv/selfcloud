# Drive pages (every signed-in user)

Overrides `MASTER.md` for the drive itself: what a member sees, and what owners see under
Drive. A user reaches only the top-level folders shared with them; inside a folder they can do
what their level allows. API: `/api/v1/folders` (folders the caller can reach), file routes
(slice e), tus uploads (slice f), downloads (slice g), `/api/v1/me`, `/me/password`.

## Home: Shared with me (`/`)

- Title "Folders". A grid of folder tiles (≥ 768px) or a list (< 768px; switchable, the choice is
  remembered per device).
- Each folder shows its name, an access badge (Can view / Can edit; owners see none), size and
  last change.
- Empty: "Nothing is shared with you yet. Ask an owner of your organisation to share a folder."

## Folder browser (`/f/{fld}/{path…}`)

- **Breadcrumbs:** Folders › Finance › 2026 › Q3. Long paths collapse the middle into "…"
  (a menu). Chevrons mirror in right-to-left.
- **Toolbar:** Upload (files, and a folder where the browser supports it), New folder, then
  view switch and sort. For **Can view**, Upload and New folder are hidden and a muted line reads
  "You can view and download in this folder."
- **List view (default):** checkbox, type icon (accent-tinted by kind), Name (`<bdi>`), Size,
  Modified, kebab. Folders first, then files, by name; sort by any column with `aria-sort`.
- **Selection:** checkbox, Shift-click for ranges, Ctrl/Cmd-click to add, Ctrl/Cmd-A for all.
  With a selection a bar replaces the toolbar: "3 selected", Download, Move to…, Copy to…,
  Rename (one only), Delete; read-only users see Download only.
- **Keyboard:** arrows move, Enter opens, Space toggles selection, F2 renames, Delete deletes
  (with confirmation), Backspace or Alt+↑ goes up. The list is one tab stop (roving tabindex).
- **Drag and drop:** drop files from the desktop onto the list or a folder row to upload there;
  a full-area drop zone appears while dragging. Moving by drag is a shortcut for Move to…, which
  always exists.
- **Move to / Copy to** dialog: a folder tree limited to folders where the user can edit;
  read-only destinations are shown disabled with "Can view only".
- **Rename** is inline in the row; Enter saves, Escape cancels, and a name conflict shows the
  error under the field.
- **Delete** confirmation states there is no trash ("Deleted files cannot be recovered").

## Upload tray

- Opens at the bottom inline-end corner once an upload starts; collapsible to a pill showing
  overall progress ("3 uploading, 42%").
- Per file: name, progress bar, "1.2 GB of 2.5 GB", speed, time left, Pause/Resume, Cancel.
  States: Waiting, Uploading, Paused, Verifying, Done, Failed (with Retry and the reason).
- Uploads go in pieces (tus); a dropped connection retries with backoff and resumes from the
  last confirmed offset. After a reload the tray lists unfinished uploads and asks the user to
  pick the same files again to resume (browsers cannot reopen a file by themselves).
- Leaving the page while uploading asks for confirmation (`beforeunload`).
- `quota_exceeded` stops new pieces and says how much space is left; `too_large` names the
  largest allowed size.
- Screen readers hear starts, completions and failures, and progress at most every 10%.

## Preview (`/f/{fld}/{path…}?preview=name`)

- A full-screen overlay: image, PDF, video, audio, text (first 1 MB) and Markdown inline;
  anything else shows the file icon, size and a Download button.
- Previous/next with arrow keys (mirrored in right-to-left), Escape closes, focus returns to the
  row. Download button always present.
- Previews are served from the content origin (`content_url`) as the API decides; the UI never
  renders user HTML or SVG inline.

## Account (`/account`)

- Name, email (read-only), Language (each in its own name), Theme (System, Light, Dark).
- Change password: current, new, confirm. Rate-limit errors are shown as text with the wait.
- Sign out, and "Sign out everywhere".
