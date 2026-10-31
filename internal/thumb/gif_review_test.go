// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"testing"
)

// gifWithFrame encodes one frame of frameRect on a logical screen of screenW by screenH. A GIF may place a frame
// anywhere on its screen, and a frame smaller than the screen is the common case for animations.
func gifWithFrame(t *testing.T, screenW, screenH int, frameRect image.Rectangle, c color.Color) []byte {
	t.Helper()
	frame := image.NewPaletted(frameRect, palette.Plan9)
	for y := frameRect.Min.Y; y < frameRect.Max.Y; y++ {
		for x := frameRect.Min.X; x < frameRect.Max.X; x++ {
			frame.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	g := &gif.GIF{
		Image:  []*image.Paletted{frame},
		Delay:  []int{0},
		Config: image.Config{ColorModel: color.Palette(palette.Plan9), Width: screenW, Height: screenH},
	}
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// L2: a GIF whose first frame is smaller than its screen is thumbnailed on the screen: the frame where it is drawn,
// and the rest of the screen shows as white.
func TestGIFFirstFrameSmallerThanScreenIsComposed(t *testing.T) {
	t.Parallel()
	red := color.RGBA{255, 0, 0, 255}
	src := gifWithFrame(t, 100, 100, image.Rect(10, 10, 60, 60), red)
	out, err := Make(src, 256, testMaxPixels)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	img := decodeOutput(t, out)
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("thumbnail = %v; want the 100 by 100 screen", b)
	}
	if r, g, b, _ := img.At(35, 35).RGBA(); r>>8 < 200 || g>>8 > 80 || b>>8 > 80 {
		t.Errorf("inside the frame = %d,%d,%d; want red", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := img.At(2, 2).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("outside the frame = %d,%d,%d; want white", r>>8, g>>8, b>>8)
	}
}

// A frame that sticks out of its screen is corrupt, not composed: the screen is the image's size.
func TestGIFFrameOutsideItsScreenIsCorrupt(t *testing.T) {
	t.Parallel()
	// Encoded on a 90 by 90 screen, then the logical screen is patched to 50 by 50 (bytes 6 to 9 of the header):
	// the frame now sticks out of it.
	src := gifWithFrame(t, 90, 90, image.Rect(40, 40, 90, 90), color.RGBA{0, 0, 255, 255})
	src[6], src[7], src[8], src[9] = 50, 0, 50, 0
	if _, err := Make(src, 256, testMaxPixels); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Make = %v, want ErrCorrupt", err)
	}
}
