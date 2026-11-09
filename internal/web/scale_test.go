// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// scaleRGBA reads one pixel of a scaled image as a colour.
func scaleRGBA(img *image.NRGBA, x, y int) color.NRGBA {
	return img.NRGBAAt(x, y)
}

// TestScaleCheckerToGrey: a black and white checker reduced to one pixel is mid grey, within one level, and opaque.
func TestScaleCheckerToGrey(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.SetNRGBA(0, 0, color.NRGBA{A: 255})
	src.SetNRGBA(1, 1, color.NRGBA{A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	src.SetNRGBA(0, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	got := scaleRGBA(scaleTo(src, 1, 1), 0, 0)
	if got.A != 255 {
		t.Errorf("alpha %d, want 255", got.A)
	}
	for _, c := range []uint8{got.R, got.G, got.B} {
		if c < 127 || c > 129 {
			t.Errorf("grey %v, want 128 within one", got)
		}
	}
}

// TestScaleTransparentBlackDoesNotBleed: an opaque white pixel beside a fully transparent black one, reduced by half, is white
// once it is laid over white. A straight average of the colours would give grey.
func TestScaleTransparentBlackDoesNotBleed(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{})
	got := scaleRGBA(scaleTo(src, 1, 1), 0, 0)
	if got.R < 254 || got.G < 254 || got.B < 254 {
		t.Errorf("the colour is %v: the transparent black bled into the white", got)
	}
	if got.A < 126 || got.A > 129 {
		t.Errorf("alpha %d, want half of 255", got.A)
	}
}

// TestScaleOpaqueBlackBesideTransparentWhiteStaysBlack: a transparent white pixel beside an opaque black one must not put a white
// halo on the edge. Averaging straight colours would give grey here, where the premultiplied average gives black.
func TestScaleOpaqueBlackBesideTransparentWhiteStaysBlack(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 0})
	got := scaleRGBA(scaleTo(src, 1, 1), 0, 0)
	if got.R > 2 || got.G > 2 || got.B > 2 {
		t.Errorf("the colour is %v: the transparent white bled into the black", got)
	}
}

// TestScaleEnlargeKeepsColours: a solid colour enlarged by two is the same colour everywhere.
func TestScaleEnlargeKeepsColours(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	out := scaleTo(src, 4, 4)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if got := scaleRGBA(out, x, y); got != (color.NRGBA{R: 200, G: 30, B: 90, A: 255}) {
				t.Fatalf("(%d,%d) is %v, want the source colour", x, y, got)
			}
		}
	}
}

// TestScaleEnlargeIsBilinear: a black and white edge enlarged by two has in-between greys on each side of the edge, not a
// block of black and a block of white.
func TestScaleEnlargeIsBilinear(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	out := scaleTo(src, 4, 1)
	if got := scaleRGBA(out, 0, 0).R; got != 0 {
		t.Errorf("the first pixel is %d, want black", got)
	}
	if got := scaleRGBA(out, 3, 0).R; got != 255 {
		t.Errorf("the last pixel is %d, want white", got)
	}
	if got := scaleRGBA(out, 1, 0).R; got < 20 || got > 110 {
		t.Errorf("pixel 1 is %d, want a dark grey", got)
	}
	if got := scaleRGBA(out, 2, 0).R; got < 150 || got > 235 {
		t.Errorf("pixel 2 is %d, want a light grey", got)
	}
}

// TestScaleReadsEveryColourType: a palette PNG and a 16-bit PNG, once decoded by the standard library, come out with their
// colours at their own size.
func TestScaleReadsEveryColourType(t *testing.T) {
	pal := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}})
	pal.SetColorIndex(0, 0, 0)
	pal.SetColorIndex(1, 0, 1)
	var pbuf bytes.Buffer
	if err := png.Encode(&pbuf, pal); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(&pbuf)
	if err != nil {
		t.Fatal(err)
	}
	if got := scaleRGBA(scaleTo(decoded, 2, 1), 0, 0); got != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("palette pixel 0 is %v, want red", got)
	}
	if got := scaleRGBA(scaleTo(decoded, 2, 1), 1, 0); got != (color.NRGBA{B: 255, A: 255}) {
		t.Errorf("palette pixel 1 is %v, want blue", got)
	}

	deep := image.NewNRGBA64(image.Rect(0, 0, 2, 1))
	deep.SetNRGBA64(0, 0, color.NRGBA64{R: 0xffff, A: 0xffff})
	deep.SetNRGBA64(1, 0, color.NRGBA64{G: 0x8000, A: 0x8000})
	var dbuf bytes.Buffer
	if err := png.Encode(&dbuf, deep); err != nil {
		t.Fatal(err)
	}
	decoded, err = png.Decode(&dbuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(*image.NRGBA64); !ok {
		t.Fatalf("a 16-bit PNG decodes to %T, want *image.NRGBA64", decoded)
	}
	if got := scaleRGBA(scaleTo(decoded, 2, 1), 0, 0); got != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("16-bit pixel 0 is %v, want red", got)
	}
	if got := scaleRGBA(scaleTo(decoded, 2, 1), 1, 0); got.G < 126 || got.G > 129 || got.A < 126 || got.A > 129 {
		t.Errorf("16-bit pixel 1 is %v, want half green at half alpha", got)
	}
}

// TestScaleKeepsAspectOfFit: fitting a 4:1 image into a 64 box gives 64 by 16, and a square gives 64 by 64.
func TestScaleKeepsAspectOfFit(t *testing.T) {
	for _, tc := range []struct{ w, h, box, wantW, wantH int }{
		{800, 200, 64, 64, 16},
		{200, 800, 64, 16, 64},
		{300, 300, 64, 64, 64},
		{800, 200, 410, 410, 103},
	} {
		w, h := fitSize(tc.w, tc.h, tc.box)
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("fitSize(%dx%d, %d) = %dx%d, want %dx%d", tc.w, tc.h, tc.box, w, h, tc.wantW, tc.wantH)
		}
	}
}
