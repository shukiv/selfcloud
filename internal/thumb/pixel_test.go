// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"image"
	"image/color"
	"image/color/palette"
	"testing"
)

// TestPixelFuncMatchesAt: each fast path reads exactly what At reads, for every pixel. The fast paths exist for speed
// (At allocates for a 16-bit or paletted pixel), so they must not change the result.
func TestPixelFuncMatchesAt(t *testing.T) {
	t.Parallel()
	const w, h = 7, 5
	rect := image.Rect(0, 0, w, h)
	nrgba := image.NewNRGBA(rect)
	nrgba64 := image.NewNRGBA64(rect)
	rgba := image.NewRGBA(rect)
	rgba64 := image.NewRGBA64(rect)
	gray := image.NewGray(rect)
	gray16 := image.NewGray16(rect)
	paletted := image.NewPaletted(rect, append(color.Palette{color.NRGBA{}, color.NRGBA{0, 0, 0, 0x80}}, palette.Plan9...))
	ycc := image.NewYCbCr(rect, image.YCbCrSubsampleRatio444)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			a := uint8(i * 37)
			nrgba.SetNRGBA(x, y, color.NRGBA{uint8(i * 11), uint8(i * 23), uint8(i * 3), a})
			nrgba64.SetNRGBA64(x, y, color.NRGBA64{uint16(i * 1111), uint16(i * 2222), uint16(i * 333), uint16(i * 4321)})
			rgba.SetRGBA(x, y, color.RGBA{uint8(i * 2), uint8(i), uint8(i * 5), 255})
			al := uint16(i * 4000)
			rgba64.SetRGBA64(x, y, color.RGBA64{uint16(i) * 3 % (al + 1), uint16(i) * 7 % (al + 1), 0, al})
			gray.SetGray(x, y, color.Gray{uint8(i * 9)})
			gray16.SetGray16(x, y, color.Gray16{uint16(i * 3001)})
			paletted.SetColorIndex(x, y, uint8(i%len(paletted.Palette)))
			ycc.Y[ycc.YOffset(x, y)] = uint8(i * 13)
			ycc.Cb[ycc.COffset(x, y)] = uint8(i * 17)
			ycc.Cr[ycc.COffset(x, y)] = uint8(i * 19)
		}
	}
	for name, img := range map[string]image.Image{"nrgba": nrgba, "nrgba64": nrgba64, "rgba": rgba, "rgba64": rgba64,
		"gray": gray, "gray16": gray16, "paletted": paletted, "ycbcr": ycc} {
		pixel := pixelFunc(img)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				gr, gg, gb, ga := pixel(x, y)
				if r != gr || g != gg || b != gb || a != ga {
					t.Fatalf("%s at %d,%d: fast path %d,%d,%d,%d, At %d,%d,%d,%d", name, x, y, gr, gg, gb, ga, r, g, b, a)
				}
			}
		}
	}
}
