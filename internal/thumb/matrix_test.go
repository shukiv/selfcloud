// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bufio"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// matrixImage is the largest image the default pixel cap allows: 7071 by 7071 is 49,999,041 pixels, under 50,000,000.
const matrixSide = 7071

// matrixSizes are the sizes the matrix decodes at. 256 and 1024 are the defaults of derived.thumb_sizes; 4096 is the
// largest a configuration may name, and it is in the matrix so that the memory floor is checked at its top too.
var matrixSizes = []int{256, 1024, 4096}

// matrixFixtures are the formats, in the matrix. Each is the largest image of its format and sampling.
var matrixFixtures = []string{
	"png8.png",   // 8-bit RGBA PNG, written here
	"png16.png",  // 16-bit RGBA PNG, written here
	"png16i.png", // 16-bit RGBA PNG, interlaced
	"g.gif",      // GIF, 256 colours
	"b444.jpg",   // baseline JPEG, 4:4:4
	"b420.jpg",   // baseline JPEG, 4:2:0
	"p444.jpg",   // progressive JPEG, 4:4:4: the largest decoder memory per pixel
	"p420.jpg",   // progressive JPEG, 4:2:0
}

// matrixCommands make the fixtures this test cannot write itself, from png8.png and png16.png. They are the
// ImageMagick commands the slice was measured with; the progressive and interlaced outputs need an encoder the
// standard library does not have.
var matrixCommands = map[string]string{
	"png16i.png": "convert png16.png -interlace Plane png16i.png",
	"g.gif":      "convert png8.png -colors 256 g.gif",
	"b444.jpg":   "convert png8.png -quality 90 -sampling-factor 4:4:4 b444.jpg",
	"b420.jpg":   "convert png8.png -quality 90 -sampling-factor 4:2:0 b420.jpg",
	"p444.jpg":   "convert png8.png -interlace Plane -quality 90 -sampling-factor 4:4:4 p444.jpg",
	"p420.jpg":   "convert png8.png -interlace Plane -quality 90 -sampling-factor 4:2:0 p420.jpg",
}

// TestMatrixDecodesEveryFormatAtTheCap is the memory matrix. It runs the child, under the server's default limits, over
// every fixture at every size in matrixSizes, and each run must succeed with the longer edge at the size asked.
//
// It is guarded: it takes minutes and about 1 GiB per child. Set SELFCLOUD_MATRIX_DIR to a directory to run it. The
// test writes png8.png and png16.png there if they are missing; the others come from matrixCommands, and the test
// stops with the command for any fixture that is absent.
func TestMatrixDecodesEveryFormatAtTheCap(t *testing.T) {
	dir := os.Getenv("SELFCLOUD_MATRIX_DIR")
	if dir == "" {
		t.Skip("set SELFCLOUD_MATRIX_DIR to a directory to run the decoder matrix")
	}
	writeGradientFixtures(t, dir)
	for _, name := range matrixFixtures {
		input, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("fixture %s: %v; make it with: %s", name, err, matrixCommands[name])
		}
		for _, size := range matrixSizes {
			res := runChild(t, input, "-size="+strconv.Itoa(size), "-max-pixels=50000000",
				"-max-input="+strconv.Itoa(50<<20), "-memory="+strconv.Itoa(1<<30), "-cpu=30")
			if res.code != ExitOK {
				t.Errorf("%s at %d: exit %d, signal %v, stderr %q, took %v", name, size, res.code, res.signal,
					res.stderr, res.took)
				continue
			}
			img := decodeOutput(t, res.stdout)
			if b := img.Bounds(); max(b.Dx(), b.Dy()) != size {
				t.Errorf("%s at %d: thumbnail is %v", name, size, b)
			}
			t.Logf("%-10s size %4d: ok in %v", name, size, res.took)
		}
	}
}

// writeGradientFixtures writes the two PNG fixtures the standard library can encode, if they are not there: an 8-bit
// and a 16-bit RGBA gradient of matrixSide by matrixSide.
func writeGradientFixtures(t *testing.T, dir string) {
	t.Helper()
	n := matrixSide
	make8 := func() image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, n, n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				img.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / (n - 1)), uint8(y * 255 / (n - 1)), uint8((x ^ y) & 0xff), 255})
			}
		}
		return img
	}
	make16 := func() image.Image {
		img := image.NewNRGBA64(image.Rect(0, 0, n, n))
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				r := uint16(x * 255 / (n - 1))
				g := uint16(y * 255 / (n - 1))
				b := uint16((x ^ y) & 0xff)
				img.SetNRGBA64(x, y, color.NRGBA64{r * 257, g * 257, b * 257, 0xffff})
			}
		}
		return img
	}
	for name, gen := range map[string]func() image.Image{"png8.png": make8, "png16.png": make16} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		if err := png.Encode(w, gen()); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
