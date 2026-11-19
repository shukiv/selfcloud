// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dav is the WebDAV protocol the read-only mount speaks (decision 236): it parses a PROPFIND
// body, builds the multistatus answer, percent-encodes hrefs and writes the finite-depth error. It
// holds no credentials, no store and no access rules; the api package wires those around it.
package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The kinds of PROPFIND body.
const (
	Allprop  = "allprop"
	Prop     = "prop"
	Propname = "propname"
)

// MaxPropfindBytes is the largest PROPFIND body the mount reads.
const MaxPropfindBytes = 64 << 10

// ErrBodyTooLarge is a PROPFIND body past MaxPropfindBytes; ErrBadPropfind is one that is not a
// propfind the mount understands.
var (
	ErrBodyTooLarge = errors.New("dav: the PROPFIND body is too large")
	ErrBadPropfind  = errors.New("dav: the PROPFIND body is not a valid propfind")
)

// Propfind is a parsed PROPFIND body: its kind, and the property names it asks for when the kind is
// Prop. An empty body is Allprop.
type Propfind struct {
	Kind  string
	Props []xml.Name
}

// propfindNode is one element of the body, with its children, enough to read the kind and the names.
type propfindNode struct {
	XMLName xml.Name
	Kids    []propfindNode `xml:",any"`
}

// ParsePropfind reads and parses the request's PROPFIND body. An empty body is allprop. A body past
// MaxPropfindBytes is ErrBodyTooLarge; anything that is not a single-kind propfind is ErrBadPropfind.
func ParsePropfind(w http.ResponseWriter, r *http.Request) (Propfind, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPropfindBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return Propfind{}, ErrBodyTooLarge
		}
		return Propfind{}, ErrBadPropfind
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return Propfind{Kind: Allprop}, nil
	}
	var root propfindNode
	if err := xml.Unmarshal(body, &root); err != nil {
		return Propfind{}, ErrBadPropfind
	}
	if root.XMLName != (xml.Name{Space: "DAV:", Local: "propfind"}) {
		return Propfind{}, ErrBadPropfind
	}
	var pf Propfind
	kinds := 0
	for _, k := range root.Kids {
		switch k.XMLName {
		case xml.Name{Space: "DAV:", Local: "allprop"}:
			pf.Kind, kinds = Allprop, kinds+1
		case xml.Name{Space: "DAV:", Local: "propname"}:
			pf.Kind, kinds = Propname, kinds+1
		case xml.Name{Space: "DAV:", Local: "prop"}:
			pf.Kind, kinds = Prop, kinds+1
			for _, p := range k.Kids {
				pf.Props = append(pf.Props, p.XMLName)
			}
		default:
			return Propfind{}, ErrBadPropfind
		}
	}
	if kinds != 1 {
		return Propfind{}, ErrBadPropfind
	}
	return pf, nil
}

// EncodeHref percent-encodes each segment of an absolute path, so a name with a space or a non-ASCII
// character becomes a valid href. The separators stay; the root "/dav/" stays "/dav/".
func EncodeHref(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
