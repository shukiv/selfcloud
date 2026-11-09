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

- `lo-beehive.pptx`: `extras/source/templates/presnt/Beehive`, "Beehive" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-blue-curve.pptx`: `extras/source/templates/presnt/Blue_Curve`, "Blue Curve" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-dna.pptx`: `extras/source/templates/presnt/DNA`, "DNA" (author named in the source: Alexander Wilms)
- `lo-focus.pptx`: `extras/source/templates/presnt/Focus`, "Focus" (author named in the source: Zirk)
- `lo-forestbird.pptx`: `extras/source/templates/presnt/Forestbird`, "Forestbird" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-inspiration.pptx`: `extras/source/templates/presnt/Inspiration`, "Inspiration" (author named in the source: Mateus Machado Luna)
- `lo-lights.pptx`: `extras/source/templates/presnt/Lights`, "Lights" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-metropolis.pptx`: `extras/source/templates/presnt/Metropolis`, "Metropolis" (its pictures are a plain background and an SVG frame) (author named in the source: Alexander Wilms)
- `lo-midnightblue.pptx`: `extras/source/templates/presnt/Midnightblue`, "Midnightblue" (author named in the source: Jun NOGATA)
- `lo-nature-illustration.pptx`: `extras/source/templates/presnt/Nature_Illustration`, "Nature Illustration" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-pencil.pptx`: `extras/source/templates/presnt/Pencil`, "Pencil" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-piano.pptx`: `extras/source/templates/presnt/Piano`, "Piano" (author named in the source by an e-mail address only; no part of the address is shipped)
- `lo-portfolio.pptx`: `extras/source/templates/presnt/Portfolio`, "Portfolio" (author named in the source: Mateus Machado Luna)
- `lo-progress.pptx`: `extras/source/templates/presnt/Progress`, "Progress" (author named in the source: William Gathoye)
- `lo-sunset.pptx`: `extras/source/templates/presnt/Sunset`, "Sunset" (author named in the source: Alexander Wilms)
- `lo-vivid.pptx`: `extras/source/templates/presnt/Vivid`, "Vivid" (author named in the source: fsanchez)

## Shipped documents (MPL-2.0)

The two letters are LibreOffice's Writer templates. Converted from `.ott` to `.docx` by LibreOffice 25.2.3.2, which is a
modification of the original file under the Mozilla Public License 2.0. The text of the licence is in
`internal/templates/licenses/MPL-2.0.txt` (a copy of LibreOffice's COPYING.MPL). Each letter has a logo placeholder and
lorem ipsum text, and no branding.

The modified files are shipped in this repository in the form Selfcloud embeds: `internal/templates/files/lo-letter-sans-serif.docx`
and `internal/templates/files/lo-letter-serif.docx`. Each keeps its document content unchanged.

Author metadata rules (lead decision, 2026-11-09). An MPL-2.0 template keeps its source's creator metadata: the sans-serif letter
keeps **Eric Lavarde** as `dc:creator`, and the serif letter has none to keep. A CC0 template is credited here, beside its file, by
its author's name as the source names it; where the source gives only an e-mail address, the entry says so, and no part of the address is shipped (the local part, the handle, is not in any shipped file either).
A CC0 template's file metadata is left as the conversion leaves it: the shipped presentations have no creator field. The last editor
field is removed from every shipped file, and no e-mail address is shipped in any of them.

Original author. The sans-serif letter's source metadata names **Eric Lavarde** as its creator (`dc:creator` and
`meta:initial-creator`). The serif letter's source metadata names no author, and the source carries no copyright notice. It is a file of the
LibreOffice project, which credits its templates to the project as a whole, so the serif letter is credited to **The Document
Foundation and the LibreOffice contributors**, under the Mozilla Public License 2.0. No individual is named for it.

- `lo-letter-serif.docx`: `extras/source/templates/officorr/Modern_business_letter_serif.ott`, "Modern business letter serif" (credited to The Document Foundation and the LibreOffice contributors, MPL-2.0)
- `lo-letter-sans-serif.docx`: `extras/source/templates/officorr/Modern_business_letter_sans_serif.ott`, "Modern business letter sans-serif" (original author: Eric Lavarde)

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
