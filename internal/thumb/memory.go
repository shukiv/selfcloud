// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

// WorstBytesPerPixel is the most memory a decode holds per source pixel, over the input formats. A progressive JPEG
// with 4:4:4 sampling keeps its coefficients (three components of 4 bytes each) and its YCbCr planes (3 bytes): 15.
// An interlaced 16-bit PNG holds an NRGBA64 (8 bytes) and its passes, which is less. 16 leaves a byte of margin.
const WorstBytesPerPixel = 16

// MemoryFloor is the address-space headroom (Limits.MemoryBytes) a child needs to decode any image of maxPixels, read an
// input of up to maxInput bytes, and write a thumbnail whose longer edge is size. The input is counted twice, since
// it is read into a buffer that may grow once; the output is 4 bytes a pixel; and 64 MiB covers the runtime's own
// working set. The config validator refuses a memory below this for each configured size.
func MemoryFloor(maxPixels, maxInput int64, size int) int64 {
	const slack = 64 << 20
	return WorstBytesPerPixel*maxPixels + 2*maxInput + 4*int64(size)*int64(size) + slack
}
