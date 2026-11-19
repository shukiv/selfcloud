// SPDX-License-Identifier: AGPL-3.0-or-later

package dav

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
)

// Status lines a propstat carries.
const (
	StatusOK       = "HTTP/1.1 200 OK"
	StatusNotFound = "HTTP/1.1 404 Not Found"
)

// Property is one property element: its name, its text value, and any child elements (a collection
// inside resourcetype). A property with no value and no children is written empty.
type Property struct {
	Name  xml.Name
	Value string
	Inner []Property
}

// Propstat is one property-status group of a response: the properties found together, and the status
// line they share.
type Propstat struct {
	Status string
	Props  []Property
}

// Response is one resource of a multistatus: its href and its propstats.
type Response struct {
	Href      string
	Propstats []Propstat
}

// Multistatus is the whole PROPFIND answer.
type Multistatus struct {
	Responses []Response
}

// TextProp builds a DAV: property with a text value.
func TextProp(local, value string) Property {
	return Property{Name: xml.Name{Space: "DAV:", Local: local}, Value: value}
}

// ResourceType builds the resourcetype property: a collection holds a collection child, a file none.
func ResourceType(collection bool) Property {
	p := Property{Name: xml.Name{Space: "DAV:", Local: "resourcetype"}}
	if collection {
		p.Inner = []Property{{Name: xml.Name{Space: "DAV:", Local: "collection"}}}
	}
	return p
}

// WriteMultistatus writes the multistatus as XML with the DAV: namespace bound to the D: prefix. A
// property in another namespace gets a generated prefix, declared on the root in first-use order.
func WriteMultistatus(w io.Writer, ms Multistatus) error {
	prefixes := map[string]string{"DAV:": "D"}
	var order []string
	var walk func([]Property)
	walk = func(props []Property) {
		for _, pr := range props {
			if pr.Name.Space != "" && pr.Name.Space != "DAV:" {
				if _, ok := prefixes[pr.Name.Space]; !ok {
					prefixes[pr.Name.Space] = fmt.Sprintf("ns%d", len(order)+1)
					order = append(order, pr.Name.Space)
				}
			}
			walk(pr.Inner)
		}
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			walk(ps.Props)
		}
	}

	b := bufio.NewWriter(w)
	b.WriteString(xml.Header)
	b.WriteString(`<D:multistatus xmlns:D="DAV:"`)
	for _, uri := range order {
		fmt.Fprintf(b, ` xmlns:%s=%q`, prefixes[uri], uri)
	}
	b.WriteString(">\n")
	for _, r := range ms.Responses {
		b.WriteString("<D:response><D:href>")
		xml.EscapeText(b, []byte(r.Href))
		b.WriteString("</D:href>")
		for _, ps := range r.Propstats {
			b.WriteString("<D:propstat><D:prop>")
			for _, pr := range ps.Props {
				writeProperty(b, pr, prefixes)
			}
			b.WriteString("</D:prop><D:status>")
			xml.EscapeText(b, []byte(ps.Status))
			b.WriteString("</D:status></D:propstat>")
		}
		b.WriteString("</D:response>\n")
	}
	b.WriteString("</D:multistatus>\n")
	return b.Flush()
}

func writeProperty(w io.Writer, pr Property, prefixes map[string]string) {
	tag := pr.Name.Local
	switch {
	case pr.Name.Space == "DAV:":
		tag = "D:" + tag
	case pr.Name.Space != "":
		tag = prefixes[pr.Name.Space] + ":" + tag
	}
	if pr.Value == "" && len(pr.Inner) == 0 {
		fmt.Fprintf(w, "<%s/>", tag)
		return
	}
	fmt.Fprintf(w, "<%s>", tag)
	xml.EscapeText(w, []byte(pr.Value))
	for _, in := range pr.Inner {
		writeProperty(w, in, prefixes)
	}
	fmt.Fprintf(w, "</%s>", tag)
}

// WriteFiniteDepthError writes the error body a PROPFIND past the supported depth gets: a
// propfind-finite-depth element, so a client knows to ask one level at a time.
func WriteFiniteDepthError(w io.Writer) error {
	b := bufio.NewWriter(w)
	b.WriteString(xml.Header)
	b.WriteString(`<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>` + "\n")
	return b.Flush()
}
