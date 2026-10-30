# Selfcloud design system (master)

> When building a page, check `design-system/selfcloud/pages/<page>.md` first. Its rules
> override this file; otherwise this file applies. Where a page names a colour token, it means the
> token of the table below (`page` is the ground that the Svelte build called `--color-background`).

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

The web interface (`web/src/theme/tokens.ts`) is the source. Components take their colours from
the Ant Design token set that the theme builds from these values, or from the palette hook; nothing
paints a raw hex value. Light and dark sets share names. The theme follows the device unless the
user chose one (key `selfcloud.theme`).

| Token | Light | Dark | Use |
|---|---|---|---|
| `page` | `#EDF0F6` | `#071531` | Page, layout ground |
| `card` | `#FFFFFF` | `#0D2142` | Panels, tables, dialogs |
| `line` / `line2` | `#DFE6F0` / `#CAD8EE` | `#1C2E4B` / `#34445E` | Dividers / control outlines |
| `text` / `text2` | `#0D2142` / `#34445E` | `#FFFFFF` / `#C3CFDF` | Body text / secondary text |
| `muted` / `faint` | `#5A6B85` / `#75869F` | `#96A6BD` / `#75869F` | Secondary text (muted) / hints only |
| `accent` | `#053D8E` | `#7097D0` | Glyphs, links, selection outline |
| `accentFill` (+ `Hover`, `Active`) | `#053D8E` | `#2A5AA6` | Ground under white text (primary button) |
| `accentText` | `#053D8E` | `#A9C1E6` | Accent-coloured text on the card |
| `accentSoft` (+ `Hover`) | `#EBF0F6` | `#1B3256` | Selected rows, active items |
| `accentBorder` (+ `Hover`) | `#CAD8EE` | `#34445E` | Focus-adjacent borders |
| `success`, `warning`, `danger` | `#059669`, `#F59E0B`, `#E11D48` | `#34D399`, `#F59E0B`, `#FB7185` | Glyphs, bars, fills |
| `successText`, `warningText`, `dangerText` | `#047857`, `#B45309`, `#BE123C` | `#34D399`, `#FECC00`, `#FB7185` | Text in a status colour |
| `*Soft`, `*Border` (per status) | see `tokens.ts` | see `tokens.ts` | Status grounds and outlines |

**Sidebar.** The brand blue in both modes: `bg` `#053D8E`, `text` `#FFFFFF`, `caption` `#A9C1E6`,
`active` `#FECC00` (the active page's pill), `onActive` `#0D2142`.

**Branding.** The operator sets tokens in `ui.brand.palette` (per theme) and `ui.brand.sidebar`, each
`#rrggbb`. An unset token keeps the default. `ui.brand.accent` alone derives the accent family
(`@ant-design/colors`). Explicit tokens win over the derived ones. An accent whose contrast is under
3:1 against a theme's page colour is not used in that theme, and the default stays.

**Contrast.** Text 4.5:1 and UI parts 3:1 in both themes. White text sits on `accentFill`, never on
`accent`, which is too light for it in dark mode.

## Typography

**Changed:** the interface uses the system font stack and ships no font files (decision 25). Arabic
and Hebrew text falls back to the system's own faces.

```css
font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial,
             "Noto Sans", sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol",
             "Noto Color Emoji";
```

| Token | Size / line-height | Weight | Use |
|---|---|---|---|
| caption | 12px / 16px | 500 | Badges, table meta (never body text) |
| small | 14px / 22px | 400 | Tables, secondary text |
| body | 14px / 22px (16px in inputs) | 400 | Body; inputs at 16px stop iOS zoom |
| section | 16px / 24px | 600 | Card titles |
| page | 24px / 32px | 600 | Page titles |

Arabic pages raise line-height by about 15%. Sizes, counts and dates use
`font-variant-numeric: tabular-nums`.

## Space, shape, elevation

Spacing on a 4px grid. Radius is 4px everywhere, the logo's corner: buttons, inputs, tags, menus,
cards. Circles stay circles (avatars, status dots, switches, progress bars). Elevation: panels have
a border and no shadow; menus, dialogs and the upload tray take the palette's `shadow`.

## Layout

- **Shell.** A Sider with the brand, the account card and the sections, 264px wide, folding to an
  88px rail (`selfcloud.navRail`). Below the `lg` breakpoint (992px) the sidebar is a Drawer,
  placed on the reading side (left in LTR, right in RTL).
- **Top bar.** Language and theme controls, the account menu; the sidebar's fold control at
  desktop width.
- **Auth pages.** From md (768px) up, a split screen: a left half on the fill-quaternary tone with the brand, a 64px gap, a three-line headline (800 weight, 40px, line-height 1.18; lines one and two in heading colour, line three in the primary colour) and a 16px tagline in secondary text (max 380px); a right half on the card colour that centres the form. Below md only the form shows, with the brand above the card. The language and theme controls sit below the form. Nothing on these pages links out.
- Breakpoints: 375, 768, 992, 1200. No horizontal page scroll at any width; tables scroll inside
  their card.

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
  catalogue, or a placeholder differs between languages. Plural keys end in `_one`, `_two`,
  `_few`, `_many`, `_other` and `_zero`, as i18next expects.
- Hebrew addresses the reader in the plural imperative, so no text assumes a gender:
  "התחברו", "העלו קבצים", "בחרו סיסמה"; never "התחבר" or "התחברי".
- Plurals follow the CLDR rules: Arabic has six plural forms and Russian three, so a catalogue
  entry with a count holds every form its language needs.

## Motion

**Changed:** the generated GSAP scroll reveal is dropped; an app should not animate content
in on scroll. Use the components' own transitions (Ant Design's motion), and nothing on lists.

- 120ms for hover and press, 180ms for menus and dialogs (enter), 120ms on exit.
- Easing `cubic-bezier(0.2, 0, 0, 1)`. Animate opacity and transform only.
- `prefers-reduced-motion: reduce` turns transitions into instant changes. Progress bars still
  move; they carry information.

## Components (shared)

- **Button:** primary (filled), secondary (outline), ghost, danger. Antd `Button` with the theme.
  Height 40px desktop, 44px on touch; icon buttons have a 44×44 hit area and an `aria-label`.
- **Focus:** the focus ring of the theme (`accent`) on every interactive element; never removed.
- **Inputs:** a visible label above, helper text below, the error below the field, in
  the danger colour with an icon, and announced. Placeholders are examples, never labels.
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
- **Icons:** Lucide (ISC licence) only, 20px, stroke 1.75 to 2. No `@ant-design/icons` imports,
  no emoji.

## Anti-patterns

- Emoji as icons; icon-only buttons without labels; colour as the only signal.
- Hover-only actions (every row action is also in a keyboard-reachable menu).
- Drag-and-drop as the only way to upload or move. An Upload button and "Move to…" always exist
  (WCAG 2.5.7).
- Physical left/right in CSS or style objects; un-isolated user text in mixed-direction pages.
- Third-party requests of any kind: fonts, icons, analytics, CDNs.
- Inline `<style>`, inline `style` attributes set as text, inline script, `dangerouslySetInnerHTML`:
  the content policy blocks them, and the build's source check refuses them.
- Layout shift on hover; spinners for more than 1s without saying what is happening.

## Pre-delivery checklist

- [ ] Contrast: text 4.5:1, large text and UI parts 3:1, in light and dark.
- [ ] Every control has a visible focus ring and is reachable by keyboard in a logical order.
- [ ] Works at 375px with no horizontal scroll; touch targets 44×44.
- [ ] Checked in Hebrew or Arabic: layout mirrors, directional icons flip, file names isolated.
- [ ] `prefers-reduced-motion` and `prefers-color-scheme` respected.
- [ ] No request leaves the server's own origin.
- [ ] Every error message comes from an API error code, translated; never raw server text.
