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

// MultistatusWriter writes a multistatus answer one response at a time, so a large listing never holds the whole
// answer in memory. The namespaces are declared on the root up front, from the property names the answer may carry;
// each response is flushed as it is written.
type MultistatusWriter struct {
	w        *bufio.Writer
	prefixes map[string]string
	err      error
}

// NewMultistatusWriter opens a multistatus on w, binding the DAV: namespace to D: and giving every other namespace
// in props a generated prefix, declared on the root in first-use order. props is the set of property names the
// responses may carry (the requested names, or all the names of a collected answer).
func NewMultistatusWriter(w io.Writer, props []xml.Name) *MultistatusWriter {
	prefixes := map[string]string{"DAV:": "D"}
	var order []string
	for _, n := range props {
		if n.Space != "" && n.Space != "DAV:" {
			if _, ok := prefixes[n.Space]; !ok {
				prefixes[n.Space] = fmt.Sprintf("ns%d", len(order)+1)
				order = append(order, n.Space)
			}
		}
	}
	b := bufio.NewWriter(w)
	b.WriteString(xml.Header)
	b.WriteString(`<D:multistatus xmlns:D="DAV:"`)
	for _, uri := range order {
		// The namespace URI is an XML attribute value: escape it as XML, not with Go quoting.
		b.WriteString(` xmlns:` + prefixes[uri] + `="`)
		xml.EscapeText(b, []byte(uri))
		b.WriteString(`"`)
	}
	b.WriteString(">\n")
	return &MultistatusWriter{w: b, prefixes: prefixes}
}

// WriteResponse writes one resource and flushes it, so the answer streams to the client as it is built.
func (m *MultistatusWriter) WriteResponse(r Response) error {
	if m.err != nil {
		return m.err
	}
	m.w.WriteString("<D:response><D:href>")
	xml.EscapeText(m.w, []byte(r.Href))
	m.w.WriteString("</D:href>")
	for _, ps := range r.Propstats {
		m.w.WriteString("<D:propstat><D:prop>")
		for _, pr := range ps.Props {
			writeProperty(m.w, pr, m.prefixes)
		}
		m.w.WriteString("</D:prop><D:status>")
		xml.EscapeText(m.w, []byte(ps.Status))
		m.w.WriteString("</D:status></D:propstat>")
	}
	m.w.WriteString("</D:response>\n")
	m.err = m.w.Flush()
	return m.err
}

// Close ends the multistatus and flushes the remainder.
func (m *MultistatusWriter) Close() error {
	if m.err != nil {
		return m.err
	}
	m.w.WriteString("</D:multistatus>\n")
	m.err = m.w.Flush()
	return m.err
}

// WriteMultistatus writes a collected multistatus as XML, response by response, with the DAV: namespace bound to the
// D: prefix and a generated prefix for every other namespace its properties carry.
func WriteMultistatus(w io.Writer, ms Multistatus) error {
	var names []xml.Name
	var walk func([]Property)
	walk = func(props []Property) {
		for _, pr := range props {
			names = append(names, pr.Name)
			walk(pr.Inner)
		}
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			walk(ps.Props)
		}
	}
	m := NewMultistatusWriter(w, names)
	for _, r := range ms.Responses {
		if err := m.WriteResponse(r); err != nil {
			return err
		}
	}
	return m.Close()
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
