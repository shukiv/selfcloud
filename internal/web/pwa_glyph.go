// SPDX-License-Identifier: AGPL-3.0-or-later

// The icons' glyph, a cloud, drawn analytically (decision 65). It is the union of three circles and a rounded rectangle,
// in the glyph's own units. Each output pixel's coverage is the share of a 4 by 4 grid of sample points that lie inside the
// shapes, so the edge is anti-aliased at every size and no bitmap is scaled.

package web

import (
	"fmt"
	"math"
)

const (
	// glyphBoxShare is the glyph's width as a share of a plain icon's width.
	glyphBoxShare = 0.60
	// glyphSafeRadius is the farthest point of the glyph from the centre of a maskable icon, as a share of its width.
	// The platforms crop a maskable icon to a circle of radius 0.40, so the glyph stays inside it.
	glyphSafeRadius = 0.36
	// glyphSamples is the sample grid per side inside each pixel: 4 by 4 gives 16 coverage levels per pixel.
	glyphSamples = 4
)

// circle is one of the cloud's bumps.
type circle struct{ x, y, r float64 }

// roundRect is the cloud's base, with rounded corners of radius r.
type roundRect struct{ x0, y0, x1, y1, r float64 }

// cloudCircles and cloudBase are the cloud, in glyph units (the glyph is about 0.58 wide and 0.41 high).
var (
	cloudCircles = []circle{{0.36, 0.52, 0.13}, {0.50, 0.42, 0.17}, {0.66, 0.50, 0.14}}
	cloudBase    = roundRect{0.22, 0.50, 0.78, 0.66, 0.08}
)

// contains reports whether (x, y) is inside the circle.
func (c circle) contains(x, y float64) bool {
	dx, dy := x-c.x, y-c.y
	return dx*dx+dy*dy <= c.r*c.r
}

// contains reports whether (x, y) is inside the rounded rectangle.
func (b roundRect) contains(x, y float64) bool {
	if x < b.x0 || x > b.x1 || y < b.y0 || y > b.y1 {
		return false
	}
	// The nearest point of the inner rectangle; a point farther than r from it is outside the rounded corner.
	cx := math.Min(math.Max(x, b.x0+b.r), b.x1-b.r)
	cy := math.Min(math.Max(y, b.y0+b.r), b.y1-b.r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= b.r*b.r
}

// glyphContains reports whether (x, y), in glyph units, is part of the cloud.
func glyphContains(x, y float64) bool {
	for _, c := range cloudCircles {
		if c.contains(x, y) {
			return true
		}
	}
	return cloudBase.contains(x, y)
}

// glyphBounds is the cloud's box in glyph units: the smallest box that holds every shape.
func glyphBounds() (x0, y0, x1, y1 float64) {
	x0, y0 = cloudBase.x0, cloudBase.y0
	x1, y1 = cloudBase.x1, cloudBase.y1
	for _, c := range cloudCircles {
		x0 = math.Min(x0, c.x-c.r)
		y0 = math.Min(y0, c.y-c.r)
		x1 = math.Max(x1, c.x+c.r)
		y1 = math.Max(y1, c.y+c.r)
	}
	return x0, y0, x1, y1
}

// glyphCentre is the centre of the cloud's box. The glyph is drawn so that this point is the icon's centre.
func glyphCentre() (float64, float64) {
	x0, y0, x1, y1 := glyphBounds()
	return (x0 + x1) / 2, (y0 + y1) / 2
}

// glyphRadius is a bound on the distance from the glyph's centre to its farthest point. Each circle contributes its
// centre's distance plus its radius; the rounded rectangle contributes its corners, which bounds its rounded ones too.
func glyphRadius() float64 {
	cx, cy := glyphCentre()
	far := 0.0
	for _, c := range cloudCircles {
		far = math.Max(far, math.Hypot(c.x-cx, c.y-cy)+c.r)
	}
	for _, p := range [][2]float64{{cloudBase.x0, cloudBase.y0}, {cloudBase.x1, cloudBase.y0}, {cloudBase.x0, cloudBase.y1}, {cloudBase.x1, cloudBase.y1}} {
		far = math.Max(far, math.Hypot(p[0]-cx, p[1]-cy))
	}
	return far
}

// glyphScale is the number of pixels in one glyph unit for an icon of size pixels. A plain icon's glyph is glyphBoxShare of
// the width; a maskable icon's glyph reaches glyphSafeRadius from the centre at most.
func glyphScale(size int, maskable bool) float64 {
	if maskable {
		return glyphSafeRadius * float64(size) / glyphRadius()
	}
	x0, _, x1, _ := glyphBounds()
	return glyphBoxShare * float64(size) / (x1 - x0)
}

// glyphSpec describes the glyph for the icons' validators: a change to any shape or share changes the ETag.
func glyphSpec() string {
	return fmt.Sprintf("circles=%v base=%v share=%v safe=%v samples=%d", cloudCircles, cloudBase, glyphBoxShare, glyphSafeRadius, glyphSamples)
}

// glyphCoverage is the share of the pixel (px, py) that the glyph covers, from 0 to 1, by a glyphSamples-square grid.
func glyphCoverage(px, py int, size int, scale float64) float64 {
	cx, cy := glyphCentre()
	half := float64(size) / 2
	hits := 0
	for j := 0; j < glyphSamples; j++ {
		for i := 0; i < glyphSamples; i++ {
			x := float64(px) + (float64(i)+0.5)/glyphSamples
			y := float64(py) + (float64(j)+0.5)/glyphSamples
			if glyphContains(cx+(x-half)/scale, cy+(y-half)/scale) {
				hits++
			}
		}
	}
	return float64(hits) / (glyphSamples * glyphSamples)
}
