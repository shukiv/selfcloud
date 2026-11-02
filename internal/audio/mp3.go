// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/binary"
)

// mp3Header is what the four bytes of an MPEG audio frame header give: the sample rate, the samples per frame, the bitrate
// and the size of the side information (which the Xing header comes after).
type mp3Header struct {
	rate, spf, kbps, sideInfo int
	crc                       bool
}

// Bitrate tables (kbit/s) for Layer III, indexed by the four bits of the header; index 0 is "free" and 15 is "bad", both
// refused. Sample rates by the two bits of the header.
var (
	mp3BitratesV1  = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3BitratesV2  = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3RatesV1     = [3]int{44100, 48000, 32000}
	mp3RatesV2     = [3]int{22050, 24000, 16000}
	mp3RatesV25    = [3]int{11025, 12000, 8000}
	mp3SamplesV1   = 1152
	mp3SamplesLSF  = 576
	mp3SideInfoV1S = 32
	mp3SideInfoV1M = 17
	mp3SideInfoLSS = 17
	mp3SideInfoLSM = 9
)

// parseMP3Header reads a frame header. It refuses anything that is not a Layer III frame with a valid bitrate and rate, so a
// stray 0xFF in the audio is not taken for a frame.
func parseMP3Header(h []byte) (mp3Header, bool) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return mp3Header{}, false
	}
	version := (h[1] >> 3) & 3 // 0 is MPEG 2.5, 1 is reserved, 2 is MPEG 2, 3 is MPEG 1
	layer := (h[1] >> 1) & 3   // 1 is Layer III
	if version == 1 || layer != 1 {
		return mp3Header{}, false
	}
	bri := h[2] >> 4
	sri := (h[2] >> 2) & 3
	if sri == 3 {
		return mp3Header{}, false
	}
	mono := h[3]>>6 == 3
	var hd mp3Header
	hd.crc = h[1]&1 == 0
	if version == 3 {
		hd.kbps = mp3BitratesV1[bri]
		hd.rate = mp3RatesV1[sri]
		hd.spf = mp3SamplesV1
		hd.sideInfo = mp3SideInfoV1S
		if mono {
			hd.sideInfo = mp3SideInfoV1M
		}
	} else {
		hd.kbps = mp3BitratesV2[bri]
		hd.rate = mp3RatesV2[sri]
		if version == 0 {
			hd.rate = mp3RatesV25[sri]
		}
		hd.spf = mp3SamplesLSF
		hd.sideInfo = mp3SideInfoLSS
		if mono {
			hd.sideInfo = mp3SideInfoLSM
		}
	}
	if hd.kbps == 0 {
		return mp3Header{}, false
	}
	return hd, true
}

// readMP3 reads an MP3 file. The ID3v2 tag gives the tags first; the ID3v1 tag at the end fills what it left empty. The
// duration comes from the Xing, Info or VBRI header of the first frame when there is one, and otherwise from the bitrate
// and the size of the audio: an estimate for a constant-rate file, which a variable-rate file without a header may miss.
func readMP3(d *reader, wantPicture bool) (Result, error) {
	res := Result{Format: "mp3"}
	h2, hasV2 := readID3v2(d, wantPicture)
	audioStart := int64(0)
	if hasV2 {
		audioStart = h2.end
		res.Tags.Title, res.Tags.Artist, res.Tags.Album = cleanText(h2.title), cleanText(h2.artist), cleanText(h2.album)
		res.Picture = bestPicture(h2.pictures)
	}
	tail := int64(0)
	if t, a, al, ok := readID3v1(d); ok {
		tail = 128
		setIfEmpty(&res.Tags.Title, cleanText(t))
		setIfEmpty(&res.Tags.Artist, cleanText(a))
		setIfEmpty(&res.Tags.Album, cleanText(al))
	}
	if audioStart >= d.size {
		if !hasV2 && tail == 0 {
			return Result{}, ErrCorrupt
		}
		return res, nil
	}
	win, err := d.at(audioStart, int(min(syncWindow, d.size-audioStart)))
	if err != nil {
		return res, nil
	}
	at := -1
	var hd mp3Header
	for i := 0; i+4 <= len(win); i++ {
		if win[i] != 0xFF {
			continue
		}
		if x, ok := parseMP3Header(win[i : i+4]); ok {
			at, hd = i, x
			break
		}
	}
	if at < 0 {
		if !hasV2 && tail == 0 {
			return Result{}, ErrCorrupt
		}
		return res, nil
	}
	frameStart := audioStart + int64(at)
	res.Tags.DurationMS = mp3Duration(d, frameStart, hd, d.size-frameStart-tail)
	return res, nil
}

// mp3Duration reads the frame count of a Xing, Info or VBRI header in the first frame, when there is one. Without a header
// it estimates from the bitrate: the audio's bytes, times eight, over the bitrate in kbit/s, gives milliseconds.
func mp3Duration(d *reader, frameStart int64, hd mp3Header, audioBytes int64) int64 {
	frame, err := d.at(frameStart, int(min(int64(hd.sideInfo+64), d.size-frameStart)))
	if err == nil {
		xing := 4 + hd.sideInfo
		if hd.crc {
			xing += 2
		}
		if xing+8 <= len(frame) && (bytes.Equal(frame[xing:xing+4], []byte("Xing")) || bytes.Equal(frame[xing:xing+4], []byte("Info"))) {
			flags := binary.BigEndian.Uint32(frame[xing+4 : xing+8])
			if flags&1 != 0 && xing+12 <= len(frame) {
				frames := int64(binary.BigEndian.Uint32(frame[xing+8 : xing+12]))
				return frames * int64(hd.spf) * 1000 / int64(hd.rate)
			}
		}
		if 36+18 <= len(frame) && bytes.Equal(frame[36:40], []byte("VBRI")) {
			frames := int64(binary.BigEndian.Uint32(frame[36+14 : 36+18]))
			return frames * int64(hd.spf) * 1000 / int64(hd.rate)
		}
	}
	if audioBytes <= 0 || hd.kbps == 0 {
		return 0
	}
	return audioBytes * 8 / int64(hd.kbps)
}
