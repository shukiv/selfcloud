// SPDX-License-Identifier: AGPL-3.0-or-later

// The SelfCloud artwork (decision 109): the logo of the default brand, the wordmark at the top of the sidebar, the mark in the
// rail, the favicon and the icons. The SVGs under brand/ are the ones in docs/brand, byte for byte (a test checks it). The mark's
// outline is read from its path data, flattened, and drawn with the same 4 by 4 sample grid as the glyph (pwa_glyph.go).

package web

import (
	"bytes"
	"embed"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed brand/*.svg
var brandFS embed.FS

const (
	// faviconPath is the browser's icon, built from the brand (decision 109).
	faviconPath = "/favicon.svg"
	// wordmarkPath is the SelfCloud wordmark with no background, for the sidebar's top.
	wordmarkPath = "/brand/wordmark.svg"
	// markPath is the mark alone: the artwork's mark, or the glyph for a custom name. The rail shows it as a mask.
	markPath = "/brand/mark.svg"
)

// brandFile returns an embedded brand file. The names are fixed, so a missing one is a build error, not a run-time one.
func brandFile(name string) []byte {
	data, err := brandFS.ReadFile("brand/" + name)
	if err != nil {
		panic("web: embedded brand file " + name + ": " + err.Error())
	}
	return data
}

// artworkLogo is the SelfCloud logo, the tile with the wordmark, for the default brand's logo.
var artworkLogo = &Logo{data: brandFile("selfcloud-logo.svg"), contentType: "image/svg+xml"}

// ArtworkLogo is the SelfCloud logo served at /brand/logo when the installation shows the artwork.
func ArtworkLogo() *Logo {
	return artworkLogo
}

// markShape is the mark's outline: its edges, in the mark file's units, and the box they span.
type markShape struct {
	edges            [][4]float64
	x0, y0, x1, y1   float64
	d                string
	centreX, centreY float64
	width            float64
}

var pathData = regexp.MustCompile(` d="([^"]+)"`)

var pathToken = regexp.MustCompile(`[MLCZ]|-?\d*\.?\d+`)

// cubicSteps is how many straight pieces each curve of the mark is cut into. A piece is at most a sixteenth of the curve, which
// is well inside a pixel at every size the icons are drawn.
const cubicSteps = 16

// parseMark reads the mark's path: M, L, C and Z, with the implicit repeats of the path grammar. Each contour's points are joined
// as closed edges, which is how an SVG fill closes a path.
func parseMark(svg []byte) markShape {
	m := pathData.FindSubmatch(svg)
	if m == nil {
		panic("web: the mark file has no path")
	}
	ms := markShape{d: string(m[1])}
	toks := pathToken.FindAllString(ms.d, -1)
	var contour [][2]float64
	var cur [2]float64
	cmd := byte('L')
	i := 0
	num := func() float64 {
		v, err := strconv.ParseFloat(toks[i], 64)
		if err != nil {
			panic("web: the mark path: " + err.Error())
		}
		i++
		return v
	}
	point := func() [2]float64 { return [2]float64{num(), num()} }
	flush := func() {
		for k := range contour {
			j := (k + 1) % len(contour)
			ms.edges = append(ms.edges, [4]float64{contour[k][0], contour[k][1], contour[j][0], contour[j][1]})
		}
		contour = nil
	}
	for i < len(toks) {
		switch toks[i] {
		case "M":
			i++
			flush()
			cmd = 'L'
			cur = point()
			contour = append(contour, cur)
			continue
		case "L":
			i++
			cmd = 'L'
			continue
		case "C":
			i++
			cmd = 'C'
			continue
		case "Z":
			i++
			flush()
			continue
		}
		if cmd == 'C' {
			c1, c2, e := point(), point(), point()
			for s := 1; s <= cubicSteps; s++ {
				t := float64(s) / cubicSteps
				u := 1 - t
				x := u*u*u*cur[0] + 3*u*u*t*c1[0] + 3*u*t*t*c2[0] + t*t*t*e[0]
				y := u*u*u*cur[1] + 3*u*u*t*c1[1] + 3*u*t*t*c2[1] + t*t*t*e[1]
				contour = append(contour, [2]float64{x, y})
			}
			cur = e
			continue
		}
		cur = point()
		contour = append(contour, cur)
	}
	flush()
	ms.x0, ms.y0, ms.x1, ms.y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, e := range ms.edges {
		for k := 0; k < 4; k += 2 {
			ms.x0, ms.x1 = math.Min(ms.x0, e[k]), math.Max(ms.x1, e[k])
			ms.y0, ms.y1 = math.Min(ms.y0, e[k+1]), math.Max(ms.y1, e[k+1])
		}
	}
	ms.centreX, ms.centreY = (ms.x0+ms.x1)/2, (ms.y0+ms.y1)/2
	ms.width = ms.x1 - ms.x0
	return ms
}

// mark is the mark's outline, read once from the embedded file.
var mark = parseMark(brandFile("selfcloud-mark.svg"))

// markScale is the pixels per mark unit for an icon or favicon of size: the mark's box is glyphBoxShare of the width, as the
// glyph's is. The outline's farthest point from the centre is inside the maskable circle at this scale (a test measures it).
func markScale(size float64) float64 {
	return glyphBoxShare * size / mark.width
}

// markCoverage returns the share of each pixel of a size by size icon that the mark covers, from 0 to 1. Each pixel is a 4 by 4
// grid of samples, as the glyph's is. Each sample row is a scanline: the crossings of the edges are sorted, and the samples
// between each pair of crossings are inside (even-odd, which is the path's fill rule).
func markCoverage(size int) []float64 {
	k := markScale(float64(size))
	half := float64(size) / 2
	type seg struct{ x0, y0, x1, y1 float64 }
	segs := make([]seg, 0, len(mark.edges))
	for _, e := range mark.edges {
		segs = append(segs, seg{
			half + (e[0]-mark.centreX)*k, half + (e[1]-mark.centreY)*k,
			half + (e[2]-mark.centreX)*k, half + (e[3]-mark.centreY)*k,
		})
	}
	n := size * glyphSamples
	hits := make([]int, size*size)
	var xs []float64
	for sy := 0; sy < n; sy++ {
		y := (float64(sy) + 0.5) / glyphSamples
		xs = xs[:0]
		for _, s := range segs {
			if (s.y0 <= y) == (s.y1 <= y) {
				continue
			}
			xs = append(xs, s.x0+(y-s.y0)*(s.x1-s.x0)/(s.y1-s.y0))
		}
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			// The sample s is at (s+0.5)/glyphSamples, so it is inside when lo <= s < hi.
			lo := int(math.Ceil(xs[i]*glyphSamples - 0.5))
			hi := int(math.Ceil(xs[i+1]*glyphSamples - 0.5))
			lo, hi = max(lo, 0), min(hi, n)
			for sx := lo; sx < hi; sx++ {
				hits[(sy/glyphSamples)*size+sx/glyphSamples]++
			}
		}
	}
	out := make([]float64, len(hits))
	for i, h := range hits {
		out[i] = float64(h) / (glyphSamples * glyphSamples)
	}
	return out
}

// figureSVG draws the picture for a viewBox of size units: the mark for the artwork, the glyph otherwise, in white, centred and
// 60% of the width. It is used in the favicon and in the mark file the rail masks.
func figureSVG(size float64, artwork bool) string {
	if artwork {
		k := markScale(size)
		return fmt.Sprintf(`<path fill="#FFFFFF" fill-rule="evenodd" transform="translate(%.2f %.2f) scale(%.5f) translate(%.2f %.2f)" d="%s"/>`,
			size/2, size/2, k, -mark.centreX, -mark.centreY, mark.d)
	}
	gx0, _, gx1, _ := glyphBounds()
	cx, cy := glyphCentre()
	k := glyphBoxShare * size / (gx1 - gx0)
	px := func(v float64) float64 { return size/2 + (v-cx)*k }
	py := func(v float64) float64 { return size/2 + (v-cy)*k }
	var b strings.Builder
	b.WriteString(`<g fill="#FFFFFF">`)
	for _, c := range cloudCircles {
		fmt.Fprintf(&b, `<circle cx="%.2f" cy="%.2f" r="%.2f"/>`, px(c.x), py(c.y), c.r*k)
	}
	fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f"/>`,
		px(cloudBase.x0), py(cloudBase.y0), (cloudBase.x1-cloudBase.x0)*k, (cloudBase.y1-cloudBase.y0)*k, cloudBase.r*k)
	b.WriteString(`</g>`)
	return b.String()
}

// faviconSVG is the favicon: the accent in a rounded square, with the figure in white (decision 109).
func faviconSVG(b brandInfo) []byte {
	var s bytes.Buffer
	fmt.Fprintf(&s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="%s"/>`, b.hex)
	s.WriteString(figureSVG(32, b.artwork))
	s.WriteString(`</svg>`)
	return s.Bytes()
}

// markSVG is the mark for the rail: the artwork's own mark file for the default brand, and the glyph for a custom name.
func markSVG(b brandInfo) []byte {
	if b.artwork {
		return brandFile("selfcloud-mark.svg")
	}
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">` + figureSVG(512, false) + `</svg>`)
}

// brandSVGs are the SVG files the server answers for a brand, by path.
func brandSVGs(b brandInfo) map[string][]byte {
	return map[string][]byte{
		faviconPath:  faviconSVG(b),
		markPath:     markSVG(b),
		wordmarkPath: brandFile("selfcloud-logo-transparent.svg"),
	}
}

// isBrandSVGPath reports whether p is one of the SVG files of the brand.
func isBrandSVGPath(p string) bool {
	return p == faviconPath || p == markPath || p == wordmarkPath
}
