// SPDX-License-Identifier: AGPL-3.0-or-later

// Package templates is the built-in starter set of new documents (decision 105): a letter, a resume and meeting notes
// (Word), a budget and an invoice (a workbook), and a three-slide deck (PowerPoint). The text is written for Selfcloud, in
// English, with [brackets] where the person fills in their own details. Nothing here is copied from another product or a
// website. Each template is built through blankdoc, which zips the parts in a fixed order, so the same template is the
// same bytes every time.
//
// The names are given in the seven languages the server ships. he and ar are machine drafts for a native speaker to check.
package templates

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/blankdoc"
)

// The kinds a template makes, as New names them.
const (
	Document     = "document"
	Spreadsheet  = "spreadsheet"
	Presentation = "presentation"
)

// Langs are the languages the names are given in. A language not listed gets the English name (Name).
var Langs = []string{"en", "es", "fr", "pt", "ru", "ar", "he"}

// Template is one built-in starting file.
type Template struct {
	ID       string // builtin:<name>, the value a create request names
	Kind     string // Document, Spreadsheet or Presentation
	Ext      string // docx, xlsx or pptx
	Category string // the gallery section: letters, resumes, reports, workbooks, presentations or designs
	names    map[string]string
	build    func() ([]byte, error)
}

// Name is the template's display name in lang, with the English name for a language that has none.
func (t Template) Name(lang string) string {
	if n := t.names[lang]; n != "" {
		return n
	}
	return t.names["en"]
}

// Build returns the template's file: a package of its extension.
func (t Template) Build() ([]byte, error) {
	return t.build()
}

// ourBuiltins are the six templates written for Selfcloud.
var ourBuiltins = []Template{
	{ID: "builtin:letter", Kind: Document, Ext: "docx", Category: "letters", build: docxFile(letterLines), names: map[string]string{
		"en": "Letter", "es": "Carta", "fr": "Lettre", "pt": "Carta", "ru": "Письмо",
		"ar": "خطاب", "he": "מכתב", // ar and he: machine drafts
	}},
	{ID: "builtin:resume", Kind: Document, Ext: "docx", Category: "resumes", build: docxFile(resumeLines), names: map[string]string{
		"en": "Resume", "es": "Currículum", "fr": "CV", "pt": "Currículo", "ru": "Резюме",
		"ar": "سيرة ذاتية", "he": "קורות חיים", // ar and he: machine drafts
	}},
	{ID: "builtin:meeting-notes", Kind: Document, Ext: "docx", Category: "reports", build: docxFile(meetingLines), names: map[string]string{
		"en": "Meeting notes", "es": "Notas de reunión", "fr": "Notes de réunion", "pt": "Notas de reunião",
		"ru": "Заметки о встрече", "ar": "ملاحظات الاجتماع", "he": "סיכום פגישה", // ar and he: machine drafts
	}},
	{ID: "builtin:budget", Kind: Spreadsheet, Ext: "xlsx", Category: "workbooks", build: xlsxFile(budgetWidths, budgetRows), names: map[string]string{
		"en": "Budget", "es": "Presupuesto", "fr": "Budget", "pt": "Orçamento", "ru": "Бюджет",
		"ar": "ميزانية", "he": "תקציב", // ar and he: machine drafts
	}},
	{ID: "builtin:invoice", Kind: Spreadsheet, Ext: "xlsx", Category: "workbooks", build: xlsxFile(invoiceWidths, invoiceRows), names: map[string]string{
		"en": "Invoice", "es": "Factura", "fr": "Facture", "pt": "Fatura", "ru": "Счёт",
		"ar": "فاتورة", "he": "חשבונית", // ar and he: machine drafts
	}},
	{ID: "builtin:slide-deck", Kind: Presentation, Ext: "pptx", Category: "presentations", build: deckFile(deckSlides), names: map[string]string{
		"en": "Slide deck", "es": "Presentación", "fr": "Diaporama", "pt": "Apresentação", "ru": "Презентация",
		"ar": "عرض شرائح", "he": "מצגת", // ar and he: machine drafts
	}},
}

// builtins is the set, in the order the listing shows it: our six, then the shipped LibreOffice templates (nt2).
var builtins = append(append([]Template(nil), ourBuiltins...), shippedBuiltins...)

// shippedBuiltins are the LibreOffice templates embedded in files/ (CREDITS.md). Their names are their English titles in every
// language, as the English name is the fallback.
var shippedBuiltins = []Template{
	shipped("lo-letter-serif", Document, "docx", "letters", "Modern business letter serif"),
	shipped("lo-letter-sans-serif", Document, "docx", "letters", "Modern business letter sans-serif"),
	shipped("lo-beehive", Presentation, "pptx", "designs", "Beehive"),
	shipped("lo-blue-curve", Presentation, "pptx", "designs", "Blue Curve"),
	shipped("lo-dna", Presentation, "pptx", "designs", "DNA"),
	shipped("lo-focus", Presentation, "pptx", "designs", "Focus"),
	shipped("lo-forestbird", Presentation, "pptx", "designs", "Forestbird"),
	shipped("lo-inspiration", Presentation, "pptx", "designs", "Inspiration"),
	shipped("lo-lights", Presentation, "pptx", "designs", "Lights"),
	shipped("lo-metropolis", Presentation, "pptx", "designs", "Metropolis"),
	shipped("lo-midnightblue", Presentation, "pptx", "designs", "Midnightblue"),
	shipped("lo-nature-illustration", Presentation, "pptx", "designs", "Nature Illustration"),
	shipped("lo-pencil", Presentation, "pptx", "designs", "Pencil"),
	shipped("lo-piano", Presentation, "pptx", "designs", "Piano"),
	shipped("lo-portfolio", Presentation, "pptx", "designs", "Portfolio"),
	shipped("lo-progress", Presentation, "pptx", "designs", "Progress"),
	shipped("lo-sunset", Presentation, "pptx", "designs", "Sunset"),
	shipped("lo-vivid", Presentation, "pptx", "designs", "Vivid"),
}

// shipped is one of the LibreOffice templates embedded in files/: its bytes as they are, with the English title as its name.
func shipped(name, kind, ext, category, title string) Template {
	return Template{ID: "builtin:" + name, Kind: kind, Ext: ext, Category: category,
		names: map[string]string{"en": title},
		build: func() ([]byte, error) { return shippedFiles.ReadFile("files/" + name + "." + ext) },
	}
}

// Thumbnail returns the picture of a built-in template, a PNG, and whether the template has one. An account's template and an
// unknown ID have none.
func Thumbnail(id string) ([]byte, bool) {
	if _, ok := Get(id); !ok {
		return nil, false
	}
	data, err := thumbFiles.ReadFile("thumbs/" + strings.TrimPrefix(id, "builtin:") + ".png")
	if err != nil {
		return nil, false
	}
	return data, true
}

// All returns the built-in set, in its order.
func All() []Template {
	return append([]Template(nil), builtins...)
}

// Get returns the built-in template with the ID, and whether there is one.
func Get(id string) (Template, bool) {
	for _, t := range builtins {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

// ByKind returns the built-in templates of one kind, in the set's order.
func ByKind(kind string) []Template {
	var out []Template
	for _, t := range builtins {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// The XML namespaces and the head the parts use. The parts themselves come from blankdoc.Parts.
const (
	xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsW     = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsR     = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsA     = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsP     = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	nsSheet = `http://schemas.openxmlformats.org/spreadsheetml/2006/main`
	relDoc  = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/`
	slideCT = `application/vnd.openxmlformats-officedocument.presentationml.slide+xml`
)

// esc escapes text for an XML element.
func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// The Word document.

// line is one paragraph: its text, whether it is bold, and its size in points (0 for the default size).
type line struct {
	text string
	bold bool
	size int
}

func documentXML(lines []line) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<w:document ` + nsW + ` ` + nsR + `><w:body>`)
	for _, l := range lines {
		b.WriteString(`<w:p>`)
		if l.text != "" {
			b.WriteString(`<w:r>`)
			if l.bold || l.size > 0 {
				b.WriteString(`<w:rPr>`)
				if l.bold {
					b.WriteString(`<w:b/>`)
				}
				if l.size > 0 {
					fmt.Fprintf(&b, `<w:sz w:val="%d"/>`, l.size*2)
				}
				b.WriteString(`</w:rPr>`)
			}
			b.WriteString(`<w:t xml:space="preserve">` + esc(l.text) + `</w:t></w:r>`)
		}
		b.WriteString(`</w:p>`)
	}
	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/>` +
		`</w:sectPr></w:body></w:document>`)
	return b.String()
}

func docxFile(lines func() []line) func() ([]byte, error) {
	return func() ([]byte, error) {
		parts := blankdoc.Parts("docx")
		if parts == nil {
			return nil, fmt.Errorf("templates: no Word parts")
		}
		parts["word/document.xml"] = documentXML(lines())
		return blankdoc.Pack(parts)
	}
}

func plain(s ...string) []line {
	out := make([]line, 0, len(s))
	for _, t := range s {
		out = append(out, line{text: t})
	}
	return out
}

func heading(s string) line {
	return line{text: s, bold: true, size: 13}
}

func letterLines() []line {
	out := []line{{text: "[Your name]", bold: true}}
	out = append(out, plain("[Street address]", "[City, postal code]", "[Email]", "", "[Date]", "", "[Recipient name]",
		"[Company]", "[Address]", "", "Dear [Recipient name],", "",
		"I am writing about [subject]. [Say in two or three sentences what you need and why it matters now.]", "",
		"[Add a paragraph with the details the reader needs: dates, amounts, or what has already been agreed.]", "",
		"[Close with the next step, such as a reply by a date, or a meeting.] Thank you for your time.", "",
		"Sincerely,", "", "[Your name]")...)
	return out
}

func resumeLines() []line {
	out := []line{{text: "[Your name]", bold: true, size: 16}}
	out = append(out, plain("[Email] | [Phone] | [City]", "")...)
	out = append(out, heading("Summary"))
	out = append(out, plain("[Two or three sentences on your experience and the work you do best.]", "")...)
	out = append(out, heading("Experience"))
	out = append(out, plain("[Job title], [Company], [Start year] to [End year]",
		"[One line on what you did and what it changed.]",
		"[Job title], [Company], [Start year] to [End year]",
		"[One line on what you did and what it changed.]", "")...)
	out = append(out, heading("Education"))
	out = append(out, plain("[Degree or certificate], [School], [Year]", "")...)
	out = append(out, heading("Skills"))
	out = append(out, plain("[Skill], [Skill], [Skill]")...)
	return out
}

func meetingLines() []line {
	out := []line{{text: "Meeting notes", bold: true, size: 16}}
	out = append(out, plain("Date: [Date]", "Time: [Start] to [End]", "Place: [Room or link]", "Attendees: [Names]", "")...)
	out = append(out, heading("Agenda"))
	out = append(out, plain("1. [First item]", "2. [Second item]", "")...)
	out = append(out, heading("Discussion"))
	out = append(out, plain("[Notes on each item, in the order they were discussed.]", "")...)
	out = append(out, heading("Decisions"))
	out = append(out, plain("[What was decided, and by whom]", "")...)
	out = append(out, heading("Action items"))
	out = append(out, plain("[Action], owner: [Name], due: [Date]")...)
	return out
}

// The workbook.

// The kinds of a cell: empty (no element), text, a number, or a formula with its value.
const (
	cellEmpty = iota
	cellText
	cellNum
	cellFormula
)

type cell struct {
	kind    int
	text    string
	num     float64
	formula string
	val     float64
}

func txt(s string) cell             { return cell{kind: cellText, text: s} }
func number(v float64) cell         { return cell{kind: cellNum, num: v} }
func calc(f string, v float64) cell { return cell{kind: cellFormula, formula: f, val: v} }
func numStr(v float64) string       { return strconv.FormatFloat(v, 'f', -1, 64) }
func colName(c int) string          { return string(rune('A' + c)) }
func emptyRow(n int) []cell         { return make([]cell, n) }

func sheetXML(widths []int, rows [][]cell) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<worksheet xmlns="` + nsSheet + `" ` + nsR + `>`)
	b.WriteString(`<cols>`)
	for i, w := range widths {
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, w)
	}
	b.WriteString(`</cols><sheetData>`)
	for r, row := range rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, cl := range row {
			ref := colName(c) + strconv.Itoa(r+1)
			switch cl.kind {
			case cellText:
				fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, esc(cl.text))
			case cellNum:
				fmt.Fprintf(&b, `<c r="%s"><v>%s</v></c>`, ref, numStr(cl.num))
			case cellFormula:
				// The value is written with the formula, so a reader shows the total before it recalculates.
				fmt.Fprintf(&b, `<c r="%s"><f>%s</f><v>%s</v></c>`, ref, esc(cl.formula), numStr(cl.val))
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func xlsxFile(widths []int, rows func() [][]cell) func() ([]byte, error) {
	return func() ([]byte, error) {
		parts := blankdoc.Parts("xlsx")
		if parts == nil {
			return nil, fmt.Errorf("templates: no workbook parts")
		}
		parts["xl/worksheets/sheet1.xml"] = sheetXML(widths, rows())
		return blankdoc.Pack(parts)
	}
}

var budgetWidths = []int{28, 14, 14, 14}

func budgetRows() [][]cell {
	items := []struct {
		name          string
		planned, done float64
	}{
		{"Rent", 1200, 1200},
		{"Groceries", 400, 380},
		{"Transport", 150, 170},
		{"Utilities", 180, 175},
		{"Savings", 300, 300},
	}
	var sumP, sumA, sumD float64
	rows := [][]cell{
		{txt("Monthly budget")},
		{txt("Category"), txt("Planned"), txt("Actual"), txt("Difference")},
	}
	for i, it := range items {
		r := i + 3 // the first item is on row 3
		rows = append(rows, []cell{txt(it.name), number(it.planned), number(it.done),
			calc(fmt.Sprintf("B%d-C%d", r, r), it.planned-it.done)})
		sumP += it.planned
		sumA += it.done
		sumD += it.planned - it.done
	}
	last := len(items) + 2 // row 7
	rows = append(rows, []cell{txt("Total"),
		calc(fmt.Sprintf("SUM(B3:B%d)", last), sumP),
		calc(fmt.Sprintf("SUM(C3:C%d)", last), sumA),
		calc(fmt.Sprintf("SUM(D3:D%d)", last), sumD)})
	return rows
}

var invoiceWidths = []int{28, 14, 14, 14}

func invoiceRows() [][]cell {
	const rate = 0.2
	type item struct {
		qty, price float64
	}
	items := []item{{1, 500}, {2, 120}, {4, 75}}
	rows := [][]cell{
		{txt("Invoice")},
		{txt("Invoice number"), txt("[Number]")},
		{txt("Date"), txt("[Date]")},
		{txt("Bill to"), txt("[Client name and address]")},
		emptyRow(0),
		{txt("Description"), txt("Quantity"), txt("Unit price"), txt("Amount")},
	}
	var subtotal float64
	for i, it := range items {
		r := i + 7 // the first item is on row 7
		rows = append(rows, []cell{txt("[Service or product]"), number(it.qty), number(it.price),
			calc(fmt.Sprintf("B%d*C%d", r, r), it.qty*it.price)})
		subtotal += it.qty * it.price
	}
	tax := subtotal * rate
	rows = append(rows,
		[]cell{txt("Subtotal"), {}, {}, calc("SUM(D7:D9)", subtotal)},
		[]cell{txt("Tax rate"), number(rate)},
		[]cell{txt("Tax"), {}, {}, calc("D10*B11", tax)},
		[]cell{txt("Total due"), {}, {}, calc("D10+D12", subtotal+tax)},
	)
	return rows
}

// The presentation.

// textShape is one text box on a slide: its top edge, its font size in points, whether it is bold, and its lines.
type textShape struct {
	y     int
	size  int
	bold  bool
	lines []string
}

// The group properties every shape tree starts with: no shapes of its own.
const groupProps = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

func slideXML(shapes []textShape) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<p:sld ` + nsA + ` ` + nsR + ` ` + nsP + `><p:cSld><p:spTree>` + groupProps)
	for i, s := range shapes {
		cy := 700000 * len(s.lines)
		bold := ""
		if s.bold {
			bold = ` b="1"`
		}
		fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Text %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, i+2, i+1)
		fmt.Fprintf(&b, `<p:spPr><a:xfrm><a:off x="838200" y="%d"/><a:ext cx="10515600" cy="%d"/></a:xfrm>`+
			`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>`, s.y, cy)
		b.WriteString(`<p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>`)
		for _, l := range s.lines {
			fmt.Fprintf(&b, `<a:p><a:r><a:rPr lang="en-US" sz="%d"%s/><a:t>%s</a:t></a:r></a:p>`, s.size*100, bold, esc(l))
		}
		b.WriteString(`</p:txBody></p:sp>`)
	}
	b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return b.String()
}

// replaceOnce replaces the one occurrence of old in s; any other count is an error, so a changed template part fails here.
func replaceOnce(s, old, new string) (string, error) {
	if strings.Count(s, old) != 1 {
		return "", fmt.Errorf("templates: the deck part does not hold the expected text once")
	}
	return strings.Replace(s, old, new, 1), nil
}

var deckSlides = [][]textShape{
	{
		{y: 2286000, size: 44, bold: true, lines: []string{"Quarterly update"}},
		{y: 3400000, size: 20, lines: []string{"[Team or presenter], [date]"}},
	},
	{
		{y: 600000, size: 36, bold: true, lines: []string{"Agenda"}},
		{y: 1700000, size: 24, lines: []string{"1. [First topic]", "2. [Second topic]", "3. Questions"}},
	},
	{
		{y: 600000, size: 36, bold: true, lines: []string{"Next steps"}},
		{y: 1700000, size: 24, lines: []string{"[Action], owner: [name], due: [date]", "[Action], owner: [name], due: [date]"}},
	},
}

// deckFile writes the slides into the blank presentation: one slide part per slide, listed in order, related to the one
// layout and registered in the content types.
func deckFile(slides [][]textShape) func() ([]byte, error) {
	return func() ([]byte, error) {
		parts := blankdoc.Parts("pptx")
		if parts == nil {
			return nil, fmt.Errorf("templates: no presentation parts")
		}
		slideRels := parts["ppt/slides/_rels/slide1.xml.rels"]
		var ids, rels, ct strings.Builder
		for i, shapes := range slides {
			n := i + 1
			parts[fmt.Sprintf("ppt/slides/slide%d.xml", n)] = slideXML(shapes)
			parts[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n)] = slideRels
			fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, 10+i)
			fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%sslide" Target="slides/slide%d.xml"/>`, 10+i, relDoc, n)
			fmt.Fprintf(&ct, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="%s"/>`, n, slideCT)
		}
		var err error
		if parts["ppt/presentation.xml"], err = replaceOnce(parts["ppt/presentation.xml"],
			`<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst>`, `<p:sldIdLst>`+ids.String()+`</p:sldIdLst>`); err != nil {
			return nil, err
		}
		// The blank deck's relationship to slide1 is replaced by the set of slides, so no relationship is left over.
		if parts["ppt/_rels/presentation.xml.rels"], err = replaceOnce(parts["ppt/_rels/presentation.xml.rels"],
			`<Relationship Id="rId2" Type="`+relDoc+`slide" Target="slides/slide1.xml"/>`, ""); err != nil {
			return nil, err
		}
		if parts["ppt/_rels/presentation.xml.rels"], err = replaceOnce(parts["ppt/_rels/presentation.xml.rels"],
			`</Relationships>`, rels.String()+`</Relationships>`); err != nil {
			return nil, err
		}
		if parts["[Content_Types].xml"], err = replaceOnce(parts["[Content_Types].xml"],
			`</Types>`, ct.String()+`</Types>`); err != nil {
			return nil, err
		}
		return blankdoc.Pack(parts)
	}
}
