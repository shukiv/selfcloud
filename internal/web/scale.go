// SPDX-License-Identifier: AGPL-3.0-or-later

// The picture scaler for the icons and the favicon (decision 119). A logo or an icon file is resized here with the standard
// library only. A reduction averages the source area each output pixel covers, the fractional edges weighted; an enlargement is
// bilinear. Both work on premultiplied colour, so a transparent pixel's colour never reaches its neighbours. The result is
// straight (not premultiplied) with its alpha kept, and each icon lays it over white or keeps it, as its use needs.
//
// The thumbnails' scaler (internal/thumb) is not this one: it lays over white and bins whole pixels.

package web

import (
	"image"
	"math"
)

// tap is one source pixel's share of an output pixel along one axis.
type tap struct {
	i int
	w float32
}

// axisTaps returns, for each output pixel along an axis, the source pixels that make it and their weights, which add up to one.
func axisTaps(srcLen, dstLen int) [][]tap {
	taps := make([][]tap, dstLen)
	s := float64(srcLen) / float64(dstLen)
	for o := range taps {
		if s >= 1 {
			lo, hi := float64(o)*s, float64(o+1)*s
			for i := int(lo); i < srcLen && float64(i) < hi; i++ {
				overlap := math.Min(hi, float64(i+1)) - math.Max(lo, float64(i))
				if overlap > 0 {
					taps[o] = append(taps[o], tap{i: i, w: float32(overlap / s)})
				}
			}
			continue
		}
		// Enlargement: the output pixel's centre, in source units, is between two source pixels.
		u := math.Min(math.Max((float64(o)+0.5)*s-0.5, 0), float64(srcLen-1))
		i0 := int(u)
		i1 := min(i0+1, srcLen-1)
		f := float32(u - float64(i0))
		taps[o] = []tap{{i: i0, w: 1 - f}, {i: i1, w: f}}
	}
	return taps
}

// to8 is a channel from 0 to 1 as a byte, clamped.
func to8(v float32) uint8 {
	return uint8(min(max(v, 0), 1)*255 + 0.5)
}

// scaleTo resamples src to dw by dh pixels. Its colours are straight and its alpha is kept.
func scaleTo(src image.Image, dw, dh int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	xt := axisTaps(sw, dw)
	yt := axisTaps(sh, dh)

	// The horizontal pass reduces each source row, premultiplied, to dw pixels. The rows are kept for the vertical pass: the
	// memory is sh by dw by four floats, which is at most 2048 by 512 for the icons.
	inter := make([]float32, sh*dw*4)
	row := make([]float32, sw*4)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x*4] = float32(r) / 0xffff
			row[x*4+1] = float32(g) / 0xffff
			row[x*4+2] = float32(bl) / 0xffff
			row[x*4+3] = float32(a) / 0xffff
		}
		for ox := 0; ox < dw; ox++ {
			var acc [4]float32
			for _, t := range xt[ox] {
				for c := 0; c < 4; c++ {
					acc[c] += t.w * row[t.i*4+c]
				}
			}
			copy(inter[(y*dw+ox)*4:], acc[:])
		}
	}

	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for oy := 0; oy < dh; oy++ {
		for ox := 0; ox < dw; ox++ {
			var acc [4]float32
			for _, t := range yt[oy] {
				p := inter[(t.i*dw+ox)*4:]
				for c := 0; c < 4; c++ {
					acc[c] += t.w * p[c]
				}
			}
			a := acc[3]
			if a <= 0 {
				continue
			}
			o := out.PixOffset(ox, oy)
			out.Pix[o] = to8(acc[0] / a)
			out.Pix[o+1] = to8(acc[1] / a)
			out.Pix[o+2] = to8(acc[2] / a)
			out.Pix[o+3] = to8(a)
		}
	}
	return out
}

// fitSize returns the size of a w by h picture fitted so that its longer side is box pixels, with its aspect kept. The shorter
// side is at least one pixel.
func fitSize(w, h, box int) (int, int) {
	if w >= h {
		return box, max(1, int(math.Round(float64(h)*float64(box)/float64(w))))
	}
	return max(1, int(math.Round(float64(w)*float64(box)/float64(h)))), box
}
