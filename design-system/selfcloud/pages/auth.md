# Sign-in and account pages

Overrides `MASTER.md` for the pages a person sees before they are signed in. API:
`/api/v1/auth/verify`, `/auth/login`, `/auth/logout`, `/auth/password/forgot`,
`/auth/password/reset`, `/api/v1/info` (product name, branding).

## Layout

- No app shell. One centred panel, max-width 400px, on the `page` colour token; the operator's logo
  and product name above it, a language picker and a theme toggle below it.
- At < 768px the panel fills the width with a 16px gutter and no border.
- Every page has one `<h1>` and one primary button. Enter submits.

## Sign in (`/login`)

- Fields: Email (`autocomplete="username"`, `inputmode="email"`), Password
  (`autocomplete="current-password"`, a show/hide toggle with `aria-pressed`).
- "Forgot password?" link sits under the password field, not after the button.
- Wrong email or password: one message, "Email or password is incorrect", above the button,
  announced. Never say which one was wrong.
- `rate_limited`: "Too many attempts. Try again in N minutes"; the button stays enabled.
- **Account picker.** When the API answers 409 because the email belongs to more than one
  account, show a list of account names as radio cards, then sign in again with the chosen
  account ID. Do not show account IDs.
- Pending (never-verified) users who try to sign in are told to use the link they were sent and
  given no other detail.

## Set your password (`/verify#token=…`)

- The token is read from the fragment, so it never reaches server logs, then removed from the
  address bar with `history.replaceState`.
- Heading "Set your password" with the account's product name. Fields: New password
  (`autocomplete="new-password"`), Confirm. A strength hint lists the rules (length at least the server's minimum, `password_min_length` in `GET /api/v1/info`, which is 8 today)
  and ticks them as they are met; the hint is text, not only colour.
- Success: signed in, straight to the home page for the role.
- `token_expired` or `not_found`: "This link has expired or was already used. Ask whoever
  invited you to send a new one." No form.

## Sign in with a link (`/sign-in#token=…`)

- Opened from an application that holds the account key; no form. Heading "Signing you in", then a status line
  "One moment…" while the one request runs.
- The token is read from the fragment and removed from the address bar before the request is made.
- Success: signed in, straight to the `next` path the server answered (`/files` when none).
- Any failure is one message: "This sign-in link has expired or was already used. Go back to the application that
  sent you and try again." Other codes from the server (`user_disabled`, `account_suspended`, `rate_limited`) show
  their own message, as elsewhere. There is no retry button for the same token.

## Forgot password (`/forgot`)

- One field, Email. After submit always show "If an account exists for this email, we sent a
  link", whether or not it exists.

## Reset password (`/reset#token=…`)

- Same as Set your password, heading "Choose a new password". Success signs out every other
  session (server-side) and says so.

## Accessibility

- `autocomplete` on every field so password managers work (WCAG 3.3.8, accessible
  authentication). Paste is never blocked.
- Errors are linked to their field with `aria-describedby`; focus moves to the first invalid field.
