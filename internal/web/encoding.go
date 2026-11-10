// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// The precompressed variants (decision 131): the build writes <file>.br and <file>.gz beside each asset that compresses, and
// the server sends the one the request takes. The coding names are the header's own, and the suffix of each variant file.
var variantSuffix = map[string]string{"br": ".br", "gzip": ".gz"}

// qWeight is a valid quality value of RFC 9110: 0 or 1, with up to three decimals.
var qWeight = regexp.MustCompile(`^(0(\.[0-9]{0,3})?|1(\.0{0,3})?)$`)

// pickEncoding chooses the coding of an asset's answer from the request's Accept-Encoding value, and says which variants the
// build holds. It returns "br", then "gzip" (the first the client takes that has a variant), or "" for the file as it is.
// Only the codings the server has are offered, so "identity" is never a choice to make: the file as it is needs no header.
func pickEncoding(accept string, hasBr, hasGzip bool) string {
	named := map[string]float64{}
	star, haveStar := 0.0, false
	for _, item := range strings.Split(accept, ",") {
		coding, params, _ := strings.Cut(item, ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding == "" {
			continue
		}
		q, ok := weightOf(params)
		if !ok {
			// A malformed weight makes that entry unusable: the coding is as if it were not named.
			continue
		}
		if coding == "*" {
			star, haveStar = q, true
			continue
		}
		named[coding] = q
	}
	// weight is the client's weight for a coding: its own, else the "*" weight for a coding the header does not name, else 0.
	weight := func(coding string) float64 {
		if v, ok := named[coding]; ok {
			return v
		}
		if haveStar {
			return star
		}
		return 0
	}
	// The coding with the higher weight wins, and br wins a tie. A weight of 0 refuses the coding.
	brQ, gzQ := 0.0, 0.0
	if hasBr {
		brQ = weight("br")
	}
	if hasGzip {
		gzQ = weight("gzip")
	}
	if brQ > 0 && brQ >= gzQ {
		return "br"
	}
	if gzQ > 0 {
		return "gzip"
	}
	return ""
}

// weightOf reads the q parameter from the parameters of one list member (the text after the coding's first ';'). A member with
// no q is weighted 1. The second result is false when the q is malformed.
func weightOf(params string) (float64, bool) {
	if params == "" {
		return 1, true
	}
	q := 1.0
	for _, param := range strings.Split(params, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(param), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		value = strings.TrimSpace(value)
		if !qWeight.MatchString(value) {
			return 0, false
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
		q = v
	}
	return q, true
}

// variants reports which precompressed variants the build holds for the file name.
func (h *Handler) variants(name string) (br, gzip bool) {
	_, errBr := fs.Stat(h.files, name+variantSuffix["br"])
	_, errGz := fs.Stat(h.files, name+variantSuffix["gzip"])
	return errBr == nil, errGz == nil
}

// isVariantName reports whether a request names a precompressed variant itself. Those are not files a person asks for: one URL
// per asset, so a direct request for one answers not found.
func isVariantName(name string) bool {
	if !strings.HasPrefix(name, "assets/") {
		return false
	}
	return strings.HasSuffix(name, variantSuffix["br"]) || strings.HasSuffix(name, variantSuffix["gzip"])
}
