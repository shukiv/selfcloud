# Credits

Selfcloud ships a few templates made by The Document Foundation's LibreOffice project. They are the starting files
offered by New > From a template (decision 105, nt2). They are in `internal/templates/files/`, and their pictures in
`internal/templates/thumbs/`. Each is embedded in the binary.

Source: LibreOffice core, commit `400f1532980458382365ab7cd420b161eea40a40` (master). Paths below are under
`extras/source/templates/` in that commit.

The six templates written for Selfcloud (`builtin:letter`, `builtin:resume`, `builtin:meeting-notes`, `builtin:budget`,
`builtin:invoice`, `builtin:slide-deck`) are Selfcloud's own, under the project's licence.

## Shipped presentations (CC0 1.0 Universal)

Each presentation's `meta.xml` states that it is "licensed under a Creative Commons 0 License" (Focus: "Creative
Commons-0 licence"). CC0 1.0 deed: https://creativecommons.org/publicdomain/zero/1.0/ . Converted from LibreOffice's
`.otp` to `.pptx` by LibreOffice 25.2.3.2.

- `lo-beehive.pptx`: `extras/source/templates/presnt/Beehive`, "Beehive"
- `lo-blue-curve.pptx`: `extras/source/templates/presnt/Blue_Curve`, "Blue Curve"
- `lo-dna.pptx`: `extras/source/templates/presnt/DNA`, "DNA"
- `lo-focus.pptx`: `extras/source/templates/presnt/Focus`, "Focus"
- `lo-forestbird.pptx`: `extras/source/templates/presnt/Forestbird`, "Forestbird"
- `lo-inspiration.pptx`: `extras/source/templates/presnt/Inspiration`, "Inspiration"
- `lo-lights.pptx`: `extras/source/templates/presnt/Lights`, "Lights"
- `lo-metropolis.pptx`: `extras/source/templates/presnt/Metropolis`, "Metropolis" (its pictures are a plain background and an SVG frame)
- `lo-midnightblue.pptx`: `extras/source/templates/presnt/Midnightblue`, "Midnightblue"
- `lo-nature-illustration.pptx`: `extras/source/templates/presnt/Nature_Illustration`, "Nature Illustration"
- `lo-pencil.pptx`: `extras/source/templates/presnt/Pencil`, "Pencil"
- `lo-piano.pptx`: `extras/source/templates/presnt/Piano`, "Piano"
- `lo-portfolio.pptx`: `extras/source/templates/presnt/Portfolio`, "Portfolio"
- `lo-progress.pptx`: `extras/source/templates/presnt/Progress`, "Progress"
- `lo-sunset.pptx`: `extras/source/templates/presnt/Sunset`, "Sunset"
- `lo-vivid.pptx`: `extras/source/templates/presnt/Vivid`, "Vivid"

## Shipped documents (MPL-2.0)

The two letters are LibreOffice's Writer templates. Converted from `.ott` to `.docx` by LibreOffice 25.2.3.2, which is a
modification of the original file under the Mozilla Public License 2.0. The text of the licence is in
`internal/templates/licenses/MPL-2.0.txt` (a copy of LibreOffice's COPYING.MPL). Each letter has a logo placeholder and
lorem ipsum text, and no branding.

- `lo-letter-serif.docx`: `extras/source/templates/officorr/Modern_business_letter_serif.ott`, "Modern business letter serif"
- `lo-letter-sans-serif.docx`: `extras/source/templates/officorr/Modern_business_letter_sans_serif.ott`, "Modern business letter sans-serif"

## Pictures

The pictures of the eighteen shipped templates (`internal/templates/thumbs/lo-<name>.png`) were rendered from the converted
files, by LibreOffice 25.2.3.2, and reduced to about 300 px wide. The pictures of our six templates were rendered the same
way, from our own files.

## Not shipped

These templates in the same LibreOffice source were left out, with the reason for each.

- `personal/CV` and `personal/Resume1page`: sample people with personal-looking details (a sample person with a date of birth
  and an address; joke sample content).
- `wizard/styles` (17 files) and `wizard/letter`, `agenda`, `report`, `fax`, `desktop`: cell-style galleries, or inputs to
  LibreOffice's wizards (field tokens such as `#subjectconst#`), not finished templates. One of them carries a sample logo.
- `extras/source/templates/presnt/Yellow_Idea` and `extras/source/templates/presnt/Growing_Liberty`: no licence stated, so the
  terms are unclear.
- `extras/source/templates/presnt/Blueprint_Plans`, `Candy`, `Grey_Elegant` and `Freshes`: CC BY-SA or Unsplash images, whose
  licences do not fit this project's.
- `extras/source/templates/presnt/Vintage`: unclear textures and terms.
