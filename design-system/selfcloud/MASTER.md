# Selfcloud design system (master)

> When building a page, check `design-system/selfcloud/pages/<page>.md` first. Its rules
> override this file; otherwise this file applies.

Generated with the ui-ux-pro-max skill on 2026-10-28 (category: File Manager & Transfer; dials:
variance 3, motion 3, density 7), then corrected for this product. Each correction is marked
**Changed:** with its reason.

## Principles

- **A tool, not a brochure.** People come to move files and share folders. Content first,
  chrome second, no decoration that does not carry meaning.
- **Swiss minimalism:** grid, clear type hierarchy, high contrast, flat surfaces, hairline
  borders, almost no shadow.
- **Works for everyone:** WCAG 2.2 AA, keyboard first-class, phone width first-class, right-to-left
  first-class.
- **Brandable:** the operator sets the product name, logo, favicon and primary colour; nothing
  else in the design depends on a particular brand.

## Colour tokens

Components use tokens only, never raw hex. Light and dark sets share names; `prefers-color-scheme`
picks one unless the user chose a theme.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--color-background` | `#F8FAFC` | `#0B1120` | Page |
| `--color-surface` | `#FFFFFF` | `#111827` | Panels, tables, dialogs |
| `--color-foreground` | `#0F172A` | `#E2E8F0` | Body text |
| `--color-muted` | `#F1F5F9` | `#1E293B` | Hover rows, secondary surfaces |
| `--color-muted-foreground` | `#475569` | `#94A3B8` | Secondary text (≥ 4.5:1 on both surfaces) |
| `--color-border` | `#E2E8F0` | `#1E293B` | Dividers (decorative) |
| `--color-input-border` | `#7C8BA1` | `#64748B` | Form-control outlines (≥ 3:1, WCAG 1.4.11) |
| `--color-primary` | `#2563EB` | `#60A5FA` | Primary actions, links, selection |
| `--color-on-primary` | `#FFFFFF` | `#0B1120` | Text on primary (5.2:1 / 7.9:1) |
| `--color-primary-soft` | `#EFF6FF` | `#172554` | Selected rows, active nav |
| `--color-accent` | `#D97706` | `#F59E0B` | File-type tint and highlights only, never text on white |
| `--color-danger` | `#DC2626` | `#F87171` | Destructive actions |
| `--color-on-danger` | `#FFFFFF` | `#0B1120` | |
| `--color-success-bg` / `-fg` | `#DCFCE7` / `#166534` | `#052E16` / `#86EFAC` | Badges: Active, Can edit |
| `--color-warning-bg` / `-fg` | `#FEF3C7` / `#92400E` | `#451A03` / `#FCD34D` | Badges: Pending, quota near full |
| `--color-ring` | `#2563EB` | `#60A5FA` | Focus ring |

**Changed:** the generated palette used amber `#D97706` with white text for the primary button
(about 3.2:1, below AA). Primary actions are blue; amber is a tint for file icons only.
**Changed:** the generated input border `#E2E8F0` was 1.2:1 against white; form controls need
3:1, hence `--color-input-border`. **Changed:** added the dark set, absent from the output.

**Branding.** The operator's accent replaces `--color-primary` (and its dark twin) in each theme where it reaches 3:1 against that theme's page background (`--color-background`, checked separately for light and dark). A theme where it fails keeps the default primary. `--color-on-primary` is black or white, whichever has the higher contrast; it always reaches 4.5:1.

## Typography

**Changed:** the generated output imported Inter from Google Fonts. Fonts are **self-hosted**:
bundled as woff2 inside the binary, so the UI makes no third-party requests and works on a LAN
without internet.

| Script | Family | Package (bundled by Vite) |
|---|---|---|
| Latin, Latin-ext, Cyrillic, Greek | Inter Variable | `@fontsource-variable/inter` |
| Hebrew | Noto Sans Hebrew Variable | `@fontsource-variable/noto-sans-hebrew` |
| Arabic | Noto Sans Arabic Variable | `@fontsource-variable/noto-sans-arabic` |

All three are under the SIL Open Font License 1.1; ship the licence text with the binary. Each
face is split by `unicode-range`, so a page downloads only the scripts it shows. `font-display:
swap`.

```css
--font-sans: "Inter Variable", "Noto Sans Hebrew Variable", "Noto Sans Arabic Variable",
             system-ui, sans-serif;
--font-mono: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace; /* IDs and keys only */
```

| Token | Size / line-height | Weight | Use |
|---|---|---|---|
| `--text-xs` | 12px / 16px | 500 | Badges, table meta (never body text) |
| `--text-sm` | 14px / 20px | 400 | Tables, secondary text |
| `--text-base` | 16px / 24px | 400 | Body, inputs (16px stops iOS zoom) |
| `--text-lg` | 18px / 28px | 600 | Section titles |
| `--text-xl` | 24px / 32px | 600 | Page titles |

Arabic pages raise line-height by about 15% (`:lang(ar)`). Sizes, counts and dates use
`font-variant-numeric: tabular-nums` so columns line up.

## Space, shape, elevation

Spacing on a 4px grid: `--space-1` 4px, `-2` 8px, `-3` 12px, `-4` 16px, `-6` 24px, `-8` 32px,
`-12` 48px. Radius: `--radius-sm` 6px (inputs, buttons), `--radius-md` 10px (panels, dialogs),
`--radius-full` (badges, avatars). Elevation: none for panels (borders instead); `--shadow-pop`
`0 8px 24px rgb(15 23 42 / 0.12)` for menus, dialogs and the upload tray only.

**Changed:** the generated cards had `cursor: pointer`, hover lift (`translateY`) and a medium
shadow. Panels here are not clickable cards; they are flat with a 1px border.

## Layout

- **App shell.** Top bar (brand, search, primary action, user menu). Side navigation 240px at
  ≥ 1024px, an icon rail at 768–1023px, a drawer below 768px. Content max-width 1280px.
- **Phone (< 768px).** One column, tables become two-line lists, primary action as a floating
  button at the inline-end bottom corner, bottom sheets instead of side drawers.
- Breakpoints: 375, 768, 1024, 1440. No horizontal page scroll at any width; `min-height: 100dvh`.
- **Changed:** the generated page pattern ("Product Demo + Features", a landing page) does not
  apply; Selfcloud has no marketing pages.

## Right-to-left and languages

These rules come from standard practice, not from the skill's database, which returned no
right-to-left match.

- `<html lang dir>` comes from the user's language; Hebrew and Arabic set `dir="rtl"`.
- CSS uses logical properties only: `margin-inline-start`, `padding-inline`, `inset-inline-end`,
  `text-align: start`, `border-inline-start`. Physical `left`/`right` is a review failure.
- Mirror directional icons (chevrons, back, arrows, "move to") with
  `:dir(rtl) .icon-dir { transform: scaleX(-1) }`. Do not mirror upload, download, play, check,
  logos or file-type icons.
- Wrap user content (file names, emails, folder names) in `<bdi>`, so a Hebrew name in an English
  page, or the reverse, keeps its order.
- Numbers, sizes and dates go through `Intl` with the user's locale; relative times ("5 minutes
  ago") through `Intl.RelativeTimeFormat`.
- The language picker shows each language in its own name: English, עברית, العربية, Español,
  Français, Português (Portugal), Русский.
- Seven languages, no more and no fewer: `en` (default and fallback), `es`, `fr`, `pt`
  (European Portuguese: spelling and usage of Portugal, never Brazil), `ru`, `ar` and `he`
  (both right to left).
- The language comes from `?lang=` on the link, then the user's `locale`, then
  `Accept-Language`, then English. Changing it in the picker saves it with `PATCH /me`.
- Strings live in `web/src/i18n/<code>.json`, one flat catalogue per language with the same
  keys. A missing key shows the English text; a test fails if any key is missing from any
  catalogue, or a placeholder differs between languages.
- Hebrew addresses the reader in the plural imperative, so no text assumes a gender:
  "התחברו", "העלו קבצים", "בחרו סיסמה"; never "התחבר" or "התחברי".
- Plurals use `Intl.PluralRules`: Arabic has six plural forms and Russian three, so a
  catalogue entry with a count holds every form its language needs.

## Motion

**Changed:** the generated GSAP scroll reveal is dropped; an app should not animate content
in on scroll. Use Svelte's built-in transitions, `|local` on lists.

- 120ms for hover and press, 180ms for menus and dialogs (enter), 120ms on exit.
- Easing `cubic-bezier(0.2, 0, 0, 1)`. Animate opacity and transform only.
- `prefers-reduced-motion: reduce` turns transitions into instant changes. Progress bars still
  move; they carry information.

## Components (shared)

- **Button:** primary (filled), secondary (outline, `--color-input-border`), ghost, danger.
  Height 40px desktop, 44px on touch; icon buttons have a 44×44 hit area and an `aria-label`.
- **Focus:** `outline: 2px solid var(--color-ring); outline-offset: 2px` on every interactive
  element; never removed.
- **Inputs:** a visible label above, helper text below, the error below the field, in
  `--color-danger` with an icon, and announced. Placeholders are examples, never labels.
- **Badges:** text plus colour, never colour alone: "Active", "Pending invite", "Disabled",
  "Suspended", "Can view", "Can edit".
- **Access control (segmented):** two options, "Can view" and "Can edit", with one line
  explaining each on first use.
- **Tables:** sticky header, 44px rows, a checkbox column for bulk actions, and a sort indicator
  with `aria-sort`. Below 768px each row becomes a two-line list item.
- **Dialogs:** focus moves in and is trapped; Escape closes; focus returns to the trigger.
  Destructive confirmations name the object and ask the user to type its name when data is lost.
- **Toasts:** bottom inline-end, `aria-live="polite"`, 5s, with an Undo where possible.
- **Upload tray:** a persistent panel at the bottom inline-end corner. Per file it shows a progress
  bar, "1.2 GB of 2.5 GB", the speed, the time left, and Pause/Resume/Cancel. Interrupted uploads
  are offered again after a reload. Screen readers hear progress at most every 10%.
- **Usage meter:** a bar with "37.2 GB of 50 GB used". It turns warning at 80% and danger at 95%,
  and the text says so too.
- **Empty states:** one sentence on what goes here and the one action that fills it.
- **Icons:** Lucide (ISC licence), as inline SVG at 20px with a 1.75 stroke. No emoji.

## Anti-patterns

- Emoji as icons; icon-only buttons without labels; colour as the only signal.
- Hover-only actions (every row action is also in a keyboard-reachable menu).
- Drag-and-drop as the only way to upload or move. An Upload button and "Move to…" always exist
  (WCAG 2.5.7).
- Physical left/right in CSS; un-isolated user text in mixed-direction pages.
- Third-party requests of any kind: fonts, icons, analytics, CDNs.
- Layout shift on hover; spinners for more than 1s without saying what is happening.

## Pre-delivery checklist

- [ ] Contrast: text 4.5:1, large text and UI parts 3:1, in light and dark.
- [ ] Every control has a visible focus ring and is reachable by keyboard in a logical order.
- [ ] Works at 375px with no horizontal scroll; touch targets 44×44.
- [ ] Checked in Hebrew or Arabic: layout mirrors, directional icons flip, file names isolated.
- [ ] `prefers-reduced-motion` and `prefers-color-scheme` respected.
- [ ] No request leaves the server's own origin.
- [ ] Every error message comes from an API error code, translated; never raw server text.
