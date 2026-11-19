// SPDX-License-Identifier: AGPL-3.0-or-later

package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// propfindRequest builds a PROPFIND request with the given body.
func propfindRequest(body string) *http.Request {
	return httptest.NewRequest("PROPFIND", "/dav/", strings.NewReader(body))
}

func TestParsePropfindEmptyBodyIsAllprop(t *testing.T) {
	t.Parallel()
	pf, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(""))
	if err != nil {
		t.Fatal(err)
	}
	if pf.Kind != Allprop {
		t.Errorf("Kind = %q, want %q", pf.Kind, Allprop)
	}
}

func TestParsePropfindKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		kind string
	}{
		{"allprop", `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`, Allprop},
		{"propname", `<D:propfind xmlns:D="DAV:"><D:propname/></D:propfind>`, Propname},
		{"prop", `<D:propfind xmlns:D="DAV:"><D:prop><D:displayname/></D:prop></D:propfind>`, Prop},
		{"default ns", `<propfind xmlns="DAV:"><allprop/></propfind>`, Allprop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pf, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if pf.Kind != c.kind {
				t.Errorf("Kind = %q, want %q", pf.Kind, c.kind)
			}
		})
	}
}

func TestParsePropfindPropNames(t *testing.T) {
	t.Parallel()
	body := `<D:propfind xmlns:D="DAV:" xmlns:X="http://example.com/ns">` +
		`<D:prop><D:displayname/><D:getcontentlength/><X:custom/></D:prop></D:propfind>`
	pf, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(body))
	if err != nil {
		t.Fatal(err)
	}
	if pf.Kind != Prop {
		t.Fatalf("Kind = %q, want prop", pf.Kind)
	}
	want := []xml.Name{
		{Space: "DAV:", Local: "displayname"},
		{Space: "DAV:", Local: "getcontentlength"},
		{Space: "http://example.com/ns", Local: "custom"},
	}
	if len(pf.Props) != len(want) {
		t.Fatalf("Props = %v, want %v", pf.Props, want)
	}
	for i, w := range want {
		if pf.Props[i] != w {
			t.Errorf("Props[%d] = %v, want %v", i, pf.Props[i], w)
		}
	}
}

func TestParsePropfindRejectsBadBodies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"not xml", `this is not xml`},
		{"wrong root", `<D:foo xmlns:D="DAV:"/>`},
		{"two kinds", `<D:propfind xmlns:D="DAV:"><D:allprop/><D:propname/></D:propfind>`},
		{"unclosed", `<D:propfind xmlns:D="DAV:"><D:prop>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(c.body))
			if !errors.Is(err, ErrBadPropfind) {
				t.Errorf("err = %v, want ErrBadPropfind", err)
			}
		})
	}
}

func TestParsePropfindTooLarge(t *testing.T) {
	t.Parallel()
	body := `<D:propfind xmlns:D="DAV:"><D:prop>` + strings.Repeat("<D:x/>", MaxPropfindBytes) + `</D:prop></D:propfind>`
	_, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(body))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("err = %v, want ErrBodyTooLarge", err)
	}
}

func TestParsePropfindDedup(t *testing.T) {
	t.Parallel()
	// A client that asks for one property twice gets it once: the names are de-duplicated.
	body := `<D:propfind xmlns:D="DAV:"><D:prop>` +
		`<D:displayname/><D:displayname/><D:getcontentlength/><D:displayname/>` +
		`</D:prop></D:propfind>`
	pf, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Props) != 2 {
		t.Fatalf("Props = %v, want displayname and getcontentlength once each", pf.Props)
	}
}

func TestParsePropfindTooManyProps(t *testing.T) {
	t.Parallel()
	// At most 64 distinct properties are answered; more is refused, so a body cannot ask for an unbounded set.
	var b strings.Builder
	b.WriteString(`<D:propfind xmlns:D="DAV:"><D:prop>`)
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&b, "<D:p%d/>", i)
	}
	b.WriteString(`</D:prop></D:propfind>`)
	pf, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(b.String()))
	if err != nil {
		t.Fatalf("64 properties refused: %v", err)
	}
	if len(pf.Props) != 64 {
		t.Fatalf("len(Props) = %d, want 64", len(pf.Props))
	}
	var b2 strings.Builder
	b2.WriteString(`<D:propfind xmlns:D="DAV:"><D:prop>`)
	for i := 0; i < 65; i++ {
		fmt.Fprintf(&b2, "<D:p%d/>", i)
	}
	b2.WriteString(`</D:prop></D:propfind>`)
	if _, err := ParsePropfind(httptest.NewRecorder(), propfindRequest(b2.String())); err == nil {
		t.Error("65 properties were accepted; want a refusal")
	}
}

func TestEncodeHref(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"/dav/", "/dav/"},
		{"/dav", "/dav"},
		{"/dav/My files", "/dav/My%20files"},
		{"/dav/My files/", "/dav/My%20files/"},
		{"/dav/שלום", "/dav/%D7%A9%D7%9C%D7%95%D7%9D"},
		{"/dav/a%b", "/dav/a%25b"},
		{"/dav/a/b c", "/dav/a/b%20c"},
	}
	for _, c := range cases {
		if got := EncodeHref(c.in); got != c.want {
			t.Errorf("EncodeHref(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// element is a generic XML node the tests decode into, so they check structure without depending on
// the writer's prefix choices.
type element struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Kids    []element  `xml:",any"`
}

func (e element) kid(local string) (element, bool) {
	for _, k := range e.Kids {
		if k.XMLName.Local == local {
			return k, true
		}
	}
	return element{}, false
}

func decodeXML(t *testing.T, b []byte) element {
	t.Helper()
	var e element
	if err := xml.Unmarshal(b, &e); err != nil {
		t.Fatalf("the answer is not valid XML: %v\n%s", err, b)
	}
	return e
}

func TestWriteMultistatus(t *testing.T) {
	t.Parallel()
	ms := Multistatus{Responses: []Response{
		{
			Href: "/dav/My%20files/",
			Propstats: []Propstat{
				{Status: StatusOK, Props: []Property{
					TextProp("displayname", "My files & <more>"),
					ResourceType(true),
				}},
				{Status: StatusNotFound, Props: []Property{
					{Name: xml.Name{Space: "DAV:", Local: "bogus"}},
					{Name: xml.Name{Space: "http://example.com/ns", Local: "custom"}},
				}},
			},
		},
	}}
	var buf bytes.Buffer
	if err := WriteMultistatus(&buf, ms); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "D:multistatus") {
		t.Errorf("the answer does not use the D: prefix:\n%s", out)
	}
	if !strings.Contains(out, "&amp;") || !strings.Contains(out, "&lt;more&gt;") {
		t.Errorf("the displayname is not XML-escaped:\n%s", out)
	}
	root := decodeXML(t, buf.Bytes())
	if root.XMLName != (xml.Name{Space: "DAV:", Local: "multistatus"}) {
		t.Fatalf("root = %v, want DAV: multistatus", root.XMLName)
	}
	resp, ok := root.kid("response")
	if !ok {
		t.Fatal("no response element")
	}
	href, _ := resp.kid("href")
	if href.Text != "/dav/My%20files/" {
		t.Errorf("href = %q", href.Text)
	}
	var found, missing []element
	for _, ps := range resp.Kids {
		if ps.XMLName.Local != "propstat" {
			continue
		}
		st, _ := ps.kid("status")
		switch st.Text {
		case StatusOK:
			found = append(found, ps)
		case StatusNotFound:
			missing = append(missing, ps)
		}
	}
	if len(found) != 1 || len(missing) != 1 {
		t.Fatalf("propstats: %d found, %d missing; want 1 and 1\n%s", len(found), len(missing), out)
	}
	prop, _ := found[0].kid("prop")
	dn, ok := prop.kid("displayname")
	if !ok || dn.Text != "My files & <more>" {
		t.Errorf("displayname = %+v", dn)
	}
	rt, ok := prop.kid("resourcetype")
	if !ok {
		t.Fatal("no resourcetype")
	}
	if _, ok := rt.kid("collection"); !ok {
		t.Errorf("resourcetype has no collection: %+v", rt)
	}
}

func TestWriteMultistatusEmptyResourceType(t *testing.T) {
	t.Parallel()
	ms := Multistatus{Responses: []Response{{
		Href:      "/dav/f.txt",
		Propstats: []Propstat{{Status: StatusOK, Props: []Property{ResourceType(false)}}},
	}}}
	var buf bytes.Buffer
	if err := WriteMultistatus(&buf, ms); err != nil {
		t.Fatal(err)
	}
	root := decodeXML(t, buf.Bytes())
	resp, _ := root.kid("response")
	ps, _ := resp.kid("propstat")
	prop, _ := ps.kid("prop")
	rt, ok := prop.kid("resourcetype")
	if !ok {
		t.Fatal("no resourcetype")
	}
	if _, ok := rt.kid("collection"); ok {
		t.Error("a file's resourcetype must not hold a collection")
	}
}

func TestWriteMultistatusNamespaceEscaped(t *testing.T) {
	t.Parallel()
	// A foreign namespace URI is an XML attribute value: an ampersand or a quote in it is escaped as XML, not
	// Go-quoted, so the answer stays valid XML and the namespace round-trips.
	const ns = `urn:test&a"b`
	ms := Multistatus{Responses: []Response{{
		Href: "/dav/f.txt",
		Propstats: []Propstat{{Status: StatusOK, Props: []Property{
			{Name: xml.Name{Space: ns, Local: "custom"}, Value: "v"},
		}}},
	}}}
	var buf bytes.Buffer
	if err := WriteMultistatus(&buf, ms); err != nil {
		t.Fatal(err)
	}
	root := decodeXML(t, buf.Bytes())
	resp, _ := root.kid("response")
	ps, _ := resp.kid("propstat")
	prop, _ := ps.kid("prop")
	custom, ok := prop.kid("custom")
	if !ok {
		t.Fatalf("no custom property:\n%s", buf.String())
	}
	if custom.XMLName.Space != ns {
		t.Errorf("namespace = %q, want %q", custom.XMLName.Space, ns)
	}
}

func TestWriteFiniteDepthError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := WriteFiniteDepthError(&buf); err != nil {
		t.Fatal(err)
	}
	root := decodeXML(t, buf.Bytes())
	if root.XMLName != (xml.Name{Space: "DAV:", Local: "error"}) {
		t.Fatalf("root = %v, want DAV: error", root.XMLName)
	}
	if _, ok := root.kid("propfind-finite-depth"); !ok {
		t.Errorf("no propfind-finite-depth element:\n%s", buf.String())
	}
}

func TestPropertyHelpers(t *testing.T) {
	t.Parallel()
	p := TextProp("displayname", "x")
	if p.Name != (xml.Name{Space: "DAV:", Local: "displayname"}) || p.Value != "x" {
		t.Errorf("TextProp = %+v", p)
	}
	col := ResourceType(true)
	if col.Name.Local != "resourcetype" || len(col.Inner) != 1 || col.Inner[0].Name.Local != "collection" {
		t.Errorf("ResourceType(true) = %+v", col)
	}
	if flat := ResourceType(false); len(flat.Inner) != 0 {
		t.Errorf("ResourceType(false) = %+v", flat)
	}
}
