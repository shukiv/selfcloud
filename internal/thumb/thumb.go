// SPDX-License-Identifier: AGPL-3.0-or-later

// Package thumb makes thumbnails of JPEG, PNG and GIF images. It reads files that any user may have
// uploaded, so it is the riskiest code in the server: it runs only in a child process (child.go),
// which the server starts with memory and CPU limits.
//
// The steps are fixed. The format is found from the first bytes, never from a name or a Content-Type.
// The header gives the dimensions, and an image whose pixel count is over the cap is refused before
// anything is allocated for its pixels. Only then is the image decoded, and its decoded size must
// match the header. The result is always a JPEG, whatever the source was.
//
// Known limit: the standard decoders do not apply the EXIF orientation of a JPEG, so a photo taken
// on its side is thumbnailed on its side.
package thumb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime"
)

// Errors returned by Header and Make. The child process maps each to an exit code.
var (
	// ErrUnsupported is an input that is not a JPEG, PNG or GIF by its first bytes.
	ErrUnsupported = errors.New("thumb: not a JPEG, PNG or GIF")
	// ErrTooLarge is an image with more pixels than the cap allows.
	ErrTooLarge = errors.New("thumb: the image has too many pixels")
	// ErrCorrupt is an image that the decoder cannot read, or whose contents do not match its header.
	ErrCorrupt = errors.New("thumb: the image cannot be decoded")
)

// jpegQuality is the quality of every thumbnail. It is part of the converter version: a change here
// needs a new converter ID, so old thumbnails are not served as current.
const jpegQuality = 85

// Format is the decoder that reads an input.
type Format string

// The three formats the standard library decodes.
const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatGIF  Format = "gif"
)

// Sniff returns the format of an input from its first bytes, and whether it is one of the three.
func Sniff(b []byte) (Format, bool) {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return FormatJPEG, true
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return FormatPNG, true
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return FormatGIF, true
	}
	return "", false
}

// Eligible reports whether a file of the stored type is thumbnailed. A file of another stored type gets no
// thumbnail, whatever its bytes are; the child checks the bytes again.
func Eligible(stored string) bool {
	mt, _, err := mime.ParseMediaType(stored)
	if err != nil {
		return false
	}
	switch mt {
	case "image/jpeg", "image/png", "image/gif":
		return true
	}
	return false
}

// Header reads the dimensions of an input without decoding its pixels, and refuses one whose pixel
// count is over maxPixels. It allocates nothing for the pixels.
func Header(b []byte, maxPixels int64) (w, h int, f Format, err error) {
	f, ok := Sniff(b)
	if !ok {
		return 0, 0, "", ErrUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, "", ErrCorrupt
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, "", ErrCorrupt
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return 0, 0, "", ErrTooLarge
	}
	return cfg.Width, cfg.Height, f, nil
}

// Fit returns the size of a w by h image scaled so that its longer edge is at most size. An image
// already within size is not enlarged.
func Fit(w, h, size int) (int, int) {
	if w <= size && h <= size {
		return w, h
	}
	if w >= h {
		return size, max(1, h*size/w)
	}
	return max(1, w*size/h), size
}

// Make decodes src and returns a JPEG whose longer edge is at most size pixels. maxPixels is the
// cap Header applies. Transparent pixels are shown on white.
func Make(src []byte, size int, maxPixels int64) ([]byte, error) {
	w, h, f, err := Header(src, maxPixels)
	if err != nil {
		return nil, err
	}
	var img image.Image
	switch f {
	case FormatJPEG:
		img, err = jpeg.Decode(bytes.NewReader(src))
	case FormatPNG:
		img, err = png.Decode(bytes.NewReader(src))
	case FormatGIF:
		img, err = decodeGIF(src, w, h)
	}
	if err != nil || img == nil {
		return nil, ErrCorrupt
	}
	if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
		return nil, ErrCorrupt
	}
	dw, dh := Fit(w, h, size)
	out := downscale(img, dw, dh)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeGIF decodes the first frame of a GIF onto its logical screen, which is w by h. A frame may be smaller than
// the screen, and is placed where the GIF puts it; the rest of the screen is transparent, which downscale shows as
// white. A frame that sticks out of the screen is corrupt.
func decodeGIF(src []byte, w, h int) (image.Image, error) {
	g, err := gif.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	frame := g.Bounds()
	screen := image.Rect(0, 0, w, h)
	if !frame.In(screen) {
		return nil, ErrCorrupt
	}
	if frame == screen {
		return g, nil
	}
	canvas := image.NewRGBA(screen)
	draw.Draw(canvas, frame, g, frame.Min, draw.Over)
	return canvas, nil
}

// downscale averages the source into a dw by dh image on white. Each source pixel goes to the output column and row
// its position falls in, so with a reduction every output pixel averages about the same number of source pixels: a
// box filter. Sums are kept in 16 bits per channel, premultiplied, so a transparent pixel adds white.
//
// The source is read one row at a time, and an output row is written as soon as its source rows are done. The memory
// is the output image and one row of sums, so it does not grow with the source.
func downscale(src image.Image, dw, dh int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	pixel := pixelFunc(src)
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	sums := make([]uint64, dw*4)
	counts := make([]uint64, dw)
	flush := func(dy int) {
		for dx := 0; dx < dw; dx++ {
			n := max(counts[dx], 1)
			a := sums[dx*4+3] / n
			// A premultiplied channel over white: the colour plus the white the transparency lets through.
			white := uint64(0xffff) - a
			o := dy*out.Stride + dx*4
			out.Pix[o] = uint8((sums[dx*4]/n + white) >> 8)
			out.Pix[o+1] = uint8((sums[dx*4+1]/n + white) >> 8)
			out.Pix[o+2] = uint8((sums[dx*4+2]/n + white) >> 8)
			out.Pix[o+3] = 0xff
		}
		clear(sums)
		clear(counts)
	}
	row := 0
	for y := 0; y < sh; y++ {
		dy := y * dh / sh
		if dy != row {
			flush(row)
			row = dy
		}
		for x := 0; x < sw; x++ {
			dx := x * dw / sw
			r, g, b, a := pixel(sb.Min.X+x, sb.Min.Y+y)
			sums[dx*4] += uint64(r)
			sums[dx*4+1] += uint64(g)
			sums[dx*4+2] += uint64(b)
			sums[dx*4+3] += uint64(a)
			counts[dx]++
		}
	}
	flush(row)
	return out
}

// ycbcr16 converts a YCbCr pixel to 16-bit RGB. It is the arithmetic of color.YCbCr.RGBA, which At uses, so the fast
// path and At agree; color.YCbCrToRGB rounds to 8 bits first and would differ by a step.
func ycbcr16(y, cb, cr uint8) (uint32, uint32, uint32) {
	yy1 := int32(y) * 0x10101
	cb1 := int32(cb) - 128
	cr1 := int32(cr) - 128
	r := yy1 + 91881*cr1
	if uint32(r)&0xff000000 == 0 {
		r >>= 8
	} else {
		r = ^(r >> 31) & 0xffff
	}
	g := yy1 - 22554*cb1 - 46802*cr1
	if uint32(g)&0xff000000 == 0 {
		g >>= 8
	} else {
		g = ^(g >> 31) & 0xffff
	}
	b := yy1 + 116130*cb1
	if uint32(b)&0xff000000 == 0 {
		b >>= 8
	} else {
		b = ^(b >> 31) & 0xffff
	}
	return uint32(r), uint32(g), uint32(b)
}

// pixelFunc returns a reader of premultiplied 16-bit channels for img. The decoder outputs the standard library makes
// are read without going through At, which allocates for each pixel of a 16-bit or paletted image.
func pixelFunc(img image.Image) func(x, y int) (r, g, b, a uint32) {
	switch m := img.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			yi := m.YOffset(x, y)
			ci := m.COffset(x, y)
			r, g, b := ycbcr16(m.Y[yi], m.Cb[ci], m.Cr[ci])
			return r, g, b, 0xffff
		}
	case *image.NRGBA:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			o := m.PixOffset(x, y)
			p := m.Pix[o : o+4 : o+4]
			a := uint32(p[3]) * 0x101
			// Premultiply: colour times alpha, in 16 bits.
			return uint32(p[0]) * 0x101 * a / 0xffff, uint32(p[1]) * 0x101 * a / 0xffff,
				uint32(p[2]) * 0x101 * a / 0xffff, a
		}
	case *image.NRGBA64:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			o := m.PixOffset(x, y)
			p := m.Pix[o : o+8 : o+8]
			a := uint32(binary.BigEndian.Uint16(p[6:8]))
			return uint32(binary.BigEndian.Uint16(p[0:2])) * a / 0xffff,
				uint32(binary.BigEndian.Uint16(p[2:4])) * a / 0xffff,
				uint32(binary.BigEndian.Uint16(p[4:6])) * a / 0xffff, a
		}
	case *image.RGBA:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			o := m.PixOffset(x, y)
			p := m.Pix[o : o+4 : o+4]
			return uint32(p[0]) * 0x101, uint32(p[1]) * 0x101, uint32(p[2]) * 0x101, uint32(p[3]) * 0x101
		}
	case *image.RGBA64:
		// Premultiplied already, as image.RGBA64 is defined.
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			c := m.RGBA64At(x, y)
			return uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
		}
	case *image.Gray:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			v := uint32(m.Pix[m.PixOffset(x, y)]) * 0x101
			return v, v, v, 0xffff
		}
	case *image.Gray16:
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			v := uint32(m.Gray16At(x, y).Y)
			return v, v, v, 0xffff
		}
	case *image.Paletted:
		// The palette has at most 256 entries: its colours are read once, in premultiplied 16-bit form.
		table := make([][4]uint32, len(m.Palette))
		for i, c := range m.Palette {
			r, g, b, a := c.RGBA()
			table[i] = [4]uint32{r, g, b, a}
		}
		return func(x, y int) (uint32, uint32, uint32, uint32) {
			t := table[m.ColorIndexAt(x, y)]
			return t[0], t[1], t[2], t[3]
		}
	}
	return func(x, y int) (uint32, uint32, uint32, uint32) {
		r, g, b, a := img.At(x, y).RGBA()
		return r, g, b, a
	}
}
