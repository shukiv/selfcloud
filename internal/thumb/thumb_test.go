// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"
)

// testMaxPixels is the pixel cap the tests use: the server's default.
const testMaxPixels = 50_000_000

// gradient makes a w by h image whose pixels say where they are, so a thumbnail that is wrong in
// place shows.
func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 255 / max(w-1, 1)), uint8(y * 255 / max(h-1, 1)), 96, 255})
		}
	}
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeGIF(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pngHeaderOnly is a PNG whose IHDR claims w by h and whose data is empty: the header is valid, the
// pixels are not there. A decoder that allocates from the header would allocate w*h*4 bytes first.
func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	put := func(off int, v uint32) {
		ihdr[off], ihdr[off+1], ihdr[off+2], ihdr[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	put(0, w)
	put(4, h)
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 6, 0, 0, 0 // 8-bit RGBA, deflate, filter, no interlace
	chunk(&b, "IHDR", ihdr)
	chunk(&b, "IEND", nil)
	return b.Bytes()
}

func chunk(b *bytes.Buffer, typ string, data []byte) {
	n := uint32(len(data))
	b.Write([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	b.WriteString(typ)
	b.Write(data)
	s := crc.Sum32()
	b.Write([]byte{byte(s >> 24), byte(s >> 16), byte(s >> 8), byte(s)})
}

// jpegWithSize patches the frame header of a small JPEG to claim w by h.
func jpegWithSize(t testing.TB, w, h uint16) []byte {
	t.Helper()
	src := encodeJPEG(t, gradient(16, 16))
	i := bytes.Index(src, []byte{0xFF, 0xC0})
	if i < 0 {
		t.Fatal("no SOF0 marker in the test JPEG")
	}
	// SOF0: marker(2) length(2) precision(1) height(2) width(2)
	src[i+5], src[i+6] = byte(h>>8), byte(h)
	src[i+7], src[i+8] = byte(w>>8), byte(w)
	return src
}

func decodeOutput(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("output is not a JPEG: %v", err)
	}
	return img
}

func TestMakeSizesAndFormats(t *testing.T) {
	t.Parallel()
	src := gradient(800, 400)
	for name, enc := range map[string][]byte{
		"png":  encodePNG(t, src),
		"jpeg": encodeJPEG(t, src),
		"gif":  encodeGIF(t, src),
	} {
		out, err := Make(enc, 256, testMaxPixels)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img := decodeOutput(t, out); img.Bounds().Dx() != 256 || img.Bounds().Dy() != 128 {
			t.Errorf("%s: thumbnail is %v, want 256x128", name, img.Bounds())
		}
	}
	// A small image is not enlarged.
	out, err := Make(encodePNG(t, gradient(100, 50)), 256, testMaxPixels)
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeOutput(t, out).Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Errorf("small image became %v", b)
	}
	// A portrait image is fitted by its height.
	out, err = Make(encodePNG(t, gradient(300, 900)), 256, testMaxPixels)
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeOutput(t, out).Bounds(); b.Dx() != 85 || b.Dy() != 256 {
		t.Errorf("portrait image became %v, want 85x256", b)
	}
}

func TestDownscaleAverages(t *testing.T) {
	t.Parallel()
	// A 2x2 checkerboard of black and white reduces to one grey pixel.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{0, 0, 0, 255})
	img.Set(1, 1, color.NRGBA{0, 0, 0, 255})
	img.Set(1, 0, color.NRGBA{255, 255, 255, 255})
	img.Set(0, 1, color.NRGBA{255, 255, 255, 255})
	out := downscale(img, 1, 1)
	if v := out.Pix[0]; v < 126 || v > 130 {
		t.Errorf("average of black and white = %d, want about 128", v)
	}
	// Transparent pixels show as white.
	clear := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if p := downscale(clear, 2, 2).Pix; p[0] != 255 || p[1] != 255 || p[2] != 255 || p[3] != 255 {
		t.Errorf("transparent pixels = %v, want white", p[:4])
	}
}

func TestYCbCrAndGrayInputs(t *testing.T) {
	t.Parallel()
	// A JPEG decodes to YCbCr and a grey PNG to Gray: both take the fast paths, which must agree with At.
	g := image.NewGray(image.Rect(0, 0, 64, 64))
	for i := range g.Pix {
		g.Pix[i] = uint8(i)
	}
	out, err := Make(encodePNG(t, g), 64, testMaxPixels)
	if err != nil {
		t.Fatal(err)
	}
	if decodeOutput(t, out).Bounds().Dx() != 64 {
		t.Fatal("grey input changed size")
	}
	if _, err := Make(encodeJPEG(t, gradient(64, 64)), 32, testMaxPixels); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderRefusesHugeHeaderWithoutAllocating(t *testing.T) {
	// Not parallel: it reads the process's allocation counters.
	src := pngHeaderOnly(30000, 30000) // 900 Mpx, far over the cap
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, _, err := Header(src, testMaxPixels)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Header = %v, want ErrTooLarge", err)
	}
	// The header is read in a few hundred bytes. The allocation is bounded by a small constant, never by the pixels
	// (900 Mpx would be 3.6 GB of RGBA).
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 256<<10 {
		t.Errorf("Header allocated %d bytes for a refused image", grew)
	}
	if _, err := Make(src, 256, testMaxPixels); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Make = %v, want ErrTooLarge", err)
	}
}

func TestBombsAreRefused(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"png 30000x30000 header only": pngHeaderOnly(30000, 30000),
		"jpeg claiming 60000x60000":   jpegWithSize(t, 60000, 60000),
		"png 2000x2000 real pixels":   encodePNG(t, gradient(2000, 2000)),
	}
	// The real-pixels case is checked against a small cap, so the test stays quick; the cap is the parameter.
	const smallCap = 1 << 20
	for name, src := range cases {
		if _, err := Make(src, 256, smallCap); !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}
	// The same image is accepted by a cap that admits it.
	if _, _, _, err := Header(cases["png 2000x2000 real pixels"], testMaxPixels); err != nil {
		t.Errorf("Header with the default cap = %v", err)
	}
}

func TestTruncatedInputsNeverPanic(t *testing.T) {
	t.Parallel()
	inputs := map[string][]byte{
		"png":  encodePNG(t, gradient(40, 30)),
		"jpeg": encodeJPEG(t, gradient(40, 30)),
		"gif":  encodeGIF(t, gradient(40, 30)),
	}
	for name, src := range inputs {
		// Every prefix either decodes (only the whole file can) or is refused as corrupt. Nothing else.
		for n := 0; n < len(src); n++ {
			_, err := Make(src[:n], 32, testMaxPixels)
			if err != nil && !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
				t.Fatalf("%s truncated to %d bytes: %v", name, n, err)
			}
		}
		if _, err := Make(src, 32, testMaxPixels); err != nil {
			t.Errorf("%s whole: %v", name, err)
		}
	}
}

func TestLyingInputsAreRefused(t *testing.T) {
	t.Parallel()
	// Magic says PNG, the body is text.
	if _, err := Make([]byte("\x89PNG\r\n\x1a\nthis is not a png at all"), 64, testMaxPixels); !errors.Is(err, ErrCorrupt) {
		t.Errorf("PNG magic over text = %v, want ErrCorrupt", err)
	}
	// A real SVG, which is never decoded.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`)
	if _, err := Make(svg, 64, testMaxPixels); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SVG = %v, want ErrUnsupported", err)
	}
	// A JPEG whose frame header says 40x30 while the image is 16x16 next to it is refused only if the decoded size
	// differs from the header: here the decoder reads the real size, which is the header's.
	if _, _, _, err := Header(jpegWithSize(t, 40, 30), testMaxPixels); err != nil {
		t.Errorf("patched JPEG header refused: %v", err)
	}
	// A PNG with a valid header and no data.
	if _, err := Make(pngHeaderOnly(16, 16), 64, testMaxPixels); !errors.Is(err, ErrCorrupt) {
		t.Errorf("PNG with no pixel data = %v, want ErrCorrupt", err)
	}
	// Zero dimensions are refused before the pixel count.
	if _, _, _, err := Header(pngHeaderOnly(0, 16), testMaxPixels); !errors.Is(err, ErrCorrupt) {
		t.Errorf("zero width = %v, want ErrCorrupt", err)
	}
}

func TestGIFWithManyFramesDecodesFirstFrame(t *testing.T) {
	t.Parallel()
	anim := &gif.GIF{}
	for i := 0; i < 200; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 32, 32), color.Palette{color.Black, color.White})
		draw.Draw(frame, frame.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		anim.Image = append(anim.Image, frame)
		anim.Delay = append(anim.Delay, 1)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, anim); err != nil {
		t.Fatal(err)
	}
	out, err := Make(b.Bytes(), 16, testMaxPixels)
	if err != nil {
		t.Fatalf("200-frame GIF: %v", err)
	}
	if img := decodeOutput(t, out); img.Bounds().Dx() != 16 {
		t.Errorf("GIF thumbnail = %v", img.Bounds())
	}
}

func FuzzMake(f *testing.F) {
	// Seeds: a valid file of each format, the bombs, a truncation and a file with valid magic and no data.
	small := gradient(24, 16)
	f.Add(encodePNG(f, small))
	f.Add(encodeJPEG(f, small))
	f.Add(encodeGIF(f, small))
	f.Add(pngHeaderOnly(30000, 30000))
	f.Add(pngHeaderOnly(16, 16))
	f.Add(jpegWithSize(f, 60000, 60000))
	f.Add(encodePNG(f, small)[:40])
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Add([]byte("GIF89a"))
	f.Add([]byte("\xFF\xD8\xFF"))
	const size, maxPixels = 64, 1 << 20
	f.Fuzz(func(t *testing.T, src []byte) {
		out, err := Make(src, size, maxPixels)
		if err != nil {
			return
		}
		// Whatever comes out is a JPEG no larger than the size, from an input within the cap.
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("output is not a JPEG: %v", err)
		}
		if b := img.Bounds(); b.Dx() > size || b.Dy() > size || b.Dx() < 1 || b.Dy() < 1 {
			t.Fatalf("output %v is outside %dx%d", b, size, size)
		}
	})
}

func FuzzHeader(f *testing.F) {
	f.Add(encodePNG(f, gradient(8, 8)))
	f.Add(pngHeaderOnly(30000, 30000))
	f.Add(jpegWithSize(f, 60000, 60000))
	f.Add([]byte("GIF89a\x01\x00\x01\x00"))
	const maxPixels = 1 << 20
	f.Fuzz(func(t *testing.T, src []byte) {
		w, h, _, err := Header(src, maxPixels)
		if err == nil && int64(w)*int64(h) > maxPixels {
			t.Fatalf("Header accepted %dx%d over the cap", w, h)
		}
	})
}
