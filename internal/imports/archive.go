// SPDX-License-Identifier: AGPL-3.0-or-later

// Package imports reads the archives that an import brings in (decision 92): zip, tar, and gzipped tar. It is the parser
// of the archive source only. It checks every entry's name and kind, and the archive's caps, from the archive's headers,
// before any of the archive's bytes are written, and Walk then reads each file's bytes in a stream. It runs no external
// tool, writes nothing to disk, and holds no entry in memory: a scan costs the same for any archive size.
package imports

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"math"
	"sort"
	"strings"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/names"
	"github.com/shukiv/selfcloud/internal/search"
)

// Format is the kind of an archive, as its first bytes say (never its name).
type Format string

// The formats an archive may be in.
const (
	Zip   Format = "zip"
	Tar   Format = "tar"
	TarGz Format = "tar.gz"
)

// Caps are the limits an archive is held to. Each is checked from the headers by Scan, and the bytes of each file are
// checked again by the import as they are written.
type Caps struct {
	MaxEntries int   // entries (files, folders) one archive may hold
	MaxDepth   int   // path segments an entry may have, its own name included
	MaxRatio   int64 // uncompressed over compressed size, for one zip entry, and for a tar.gz as a whole
	MaxFile    int64 // the largest file, as upload.max_file_size
	MaxTotal   int64 // the uncompressed bytes of all the files
	// MaxPathBytes is the bytes of all the entries' full paths. Zero is no cap; the settings never give zero (round 4, N2).
	MaxPathBytes int64
}

// ratioFloor is the compressed size a ratio is measured against when a file packs to less than it, so a tiny file that
// packs to a few bytes is not refused for its ratio.
const ratioFloor = 1024

// Name is one segment of an entry's path: the name as it is stored (NFC), and its clash key.
type Name struct {
	Name string
	Key  string
}

// Entry is one entry of an archive, as Scan and Walk read it.
type Entry struct {
	Index  int    // 1-based, in archive order, counting files and folders only
	Path   string // the names joined with "/"
	Key    string // the clash keys joined with "/"
	Names  []Name
	Dir    bool
	Size   int64 // the declared uncompressed size of a file; 0 for a folder
	Packed int64 // the declared compressed size of a zip entry; 0 for a tar entry
	// Zipped is set for a zip entry, whose size is held to the ratio against Packed. A tar entry has no compressed size of
	// its own: a tar's ratio is measured on the stream as a whole (round 3, F12).
	Zipped bool
}

// Summary is what Scan found: the format and the counts, with the sum of the declared sizes.
type Summary struct {
	Format  Format
	Entries int
	Files   int
	Dirs    int
	Bytes   int64
}

// refuse is the refusal of an archive or one of its entries. The code is an API code: a name or kind the archive may not
// hold is invalid_path, a cap is too_large with the cap as the reason, and a file that is not an archive is
// unsupported_media_type.
func refuse(code apierr.Code, msg, reason string) error {
	e := apierr.New(code, msg)
	if reason != "" {
		e = e.With("reason", reason)
	}
	return e
}

// Detect reads the format of the archive of size bytes in r, from its first bytes.
func Detect(r io.ReaderAt, size int64) (Format, error) {
	var head [4]byte
	if size >= int64(len(head)) {
		if _, err := r.ReadAt(head[:], 0); err != nil && err != io.EOF {
			return "", err
		}
		if bytes.Equal(head[:], []byte("PK\x03\x04")) || bytes.Equal(head[:], []byte("PK\x05\x06")) {
			return Zip, nil
		}
		if head[0] == 0x1f && head[1] == 0x8b {
			return TarGz, nil
		}
	}
	var magic [5]byte
	if size >= 512 {
		if _, err := r.ReadAt(magic[:], 257); err != nil && err != io.EOF {
			return "", err
		}
		if string(magic[:]) == "ustar" {
			return Tar, nil
		}
	}
	return "", refuse(apierr.UnsupportedMedia, "the file is not a zip or tar archive", "")
}

// Scan reads the headers of the archive of size bytes in r and checks them against caps. It returns the counts, or the
// first refusal. No entry's bytes are kept; a tar.gz is decompressed once, to read its headers.
func Scan(r io.ReaderAt, size int64, caps Caps) (Summary, error) {
	f, err := Detect(r, size)
	if err != nil {
		return Summary{}, err
	}
	s := &scanner{caps: caps, sum: Summary{Format: f}}
	switch f {
	case Zip:
		err = s.zip(r, size)
	default:
		err = s.tar(r, size, f)
	}
	if err != nil {
		return Summary{}, err
	}
	if err := s.clashes(); err != nil {
		return Summary{}, err
	}
	if f == TarGz && OverRatio(s.sum.Bytes, size, caps.MaxRatio) {
		return Summary{}, refuse(apierr.TooLarge, "the archive expands more than it may", "ratio")
	}
	return s.sum, nil
}

type scanner struct {
	caps      Caps
	keys      []pathKey // one per entry: the path for the clash check, and whether it is a folder (round 4, N2)
	pathBytes int64     // the bytes of the keys, held to caps.MaxPathBytes
	sum       Summary
}

// pathKey is an entry's path as the clash check reads it: its segments joined by NUL, which no name holds, so that the
// entries under a path sort right after it.
type pathKey struct {
	path string
	dir  bool
}

// clashes refuses an archive in which one path is both a file and a folder, or is listed twice (round 3, F7). A folder is
// a folder of the archive whether or not it has an entry of its own, so a file that is a folder's path refuses the archive
// in either order. The keys are sorted, and the entries under a path follow it at once, so each path is compared with the
// next one only: the scan keeps one key per entry, not one per folder above it (round 4, N2).
func (s *scanner) clashes() error {
	sort.Slice(s.keys, func(i, j int) bool { return s.keys[i].path < s.keys[j].path })
	for i := 0; i+1 < len(s.keys); i++ {
		a, b := s.keys[i], s.keys[i+1]
		if a.path == b.path {
			// A folder listed twice is harmless. A file listed twice, or a file and a folder with one path, is not.
			if !(a.dir && b.dir) {
				return refuse(apierr.InvalidPath, "the archive lists one path twice", "duplicate")
			}
			continue
		}
		if !a.dir && strings.HasPrefix(b.path, a.path+"\x00") {
			return refuse(apierr.InvalidPath, "the archive lists one path twice", "duplicate")
		}
	}
	return nil
}

// add checks one entry against the caps and counts it. Its path is kept for the clash check, within max_path_bytes.
func (s *scanner) add(e Entry) error {
	if s.sum.Entries+1 > s.caps.MaxEntries {
		return refuse(apierr.TooLarge, "the archive holds more entries than it may", "entries")
	}
	if len(e.Names) > s.caps.MaxDepth {
		return refuse(apierr.TooLarge, "an entry sits deeper than the archive may nest", "depth")
	}
	// Zero is no cap; the settings never give zero.
	if s.caps.MaxPathBytes > 0 && s.pathBytes+int64(len(e.Key)) > s.caps.MaxPathBytes {
		return refuse(apierr.TooLarge, "the archive's paths hold more bytes than it may", "path_bytes")
	}
	s.pathBytes += int64(len(e.Key))
	s.keys = append(s.keys, pathKey{path: strings.ReplaceAll(e.Key, "/", "\x00"), dir: e.Dir})
	if !e.Dir {
		if e.Size < 0 || e.Packed < 0 {
			return refuse(apierr.TooLarge, "an entry declares a size no archive can hold", "size")
		}
		if e.Size > s.caps.MaxFile {
			return refuse(apierr.TooLarge, "a file in the archive is larger than the largest file allowed", "file_size")
		}
		if e.Zipped && OverRatio(e.Size, e.Packed, s.caps.MaxRatio) {
			return refuse(apierr.TooLarge, "a file in the archive expands more than it may", "ratio")
		}
		if e.Size > s.caps.MaxTotal-s.sum.Bytes {
			return refuse(apierr.TooLarge, "the archive's files hold more bytes than it may", "total")
		}
		s.sum.Bytes += e.Size
		s.sum.Files++
	} else {
		s.sum.Dirs++
	}
	s.sum.Entries++
	return nil
}

func (s *scanner) zip(r io.ReaderAt, size int64) error {
	n, ok := search.ZipEntryCount(r, size, s.caps.MaxEntries)
	if !ok {
		return refuse(apierr.UnsupportedMedia, "the zip's structure is not valid", "")
	}
	if n > uint64(s.caps.MaxEntries) {
		return refuse(apierr.TooLarge, "the archive holds more entries than it may", "entries")
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return refuse(apierr.UnsupportedMedia, "the zip's structure is not valid", "")
	}
	if len(zr.File) > s.caps.MaxEntries {
		return refuse(apierr.TooLarge, "the archive holds more entries than it may", "entries")
	}
	for _, f := range zr.File {
		usz, pck, err := zipSizes(f, size)
		if err != nil {
			return err
		}
		e, err := entryOf(f.Name, f.FileInfo().IsDir(), f.Mode(), usz, pck)
		if err != nil {
			return err
		}
		if e.Dir && f.UncompressedSize64 != 0 {
			return refuse(apierr.InvalidPath, "a folder in the archive has contents", "")
		}
		if f.Flags&0x1 != 0 {
			return refuse(apierr.InvalidPath, "an entry of the archive is encrypted", "")
		}
		e.Index = s.sum.Entries + 1
		e.Zipped = true
		if err := s.add(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) tar(r io.ReaderAt, size int64, f Format) error {
	cr := &countReader{limit: tarLimit(size, s.caps, f)}
	tr, closeFn, err := openTar(r, size, f, cr)
	if err != nil {
		return err
	}
	defer closeFn()
	skipped := 0
	for {
		h, err := tr.Next()
		if cr.over {
			return refuse(apierr.TooLarge, "the archive expands more than it may", "ratio")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return refuse(apierr.UnsupportedMedia, "the tar archive is not valid", "")
		}
		e, ok, err := entryOfTar(h)
		if err != nil {
			return err
		}
		if !ok {
			// A header that is not an entry counts too: headers cannot carry what the caps do not see.
			skipped++
			if skipped > s.caps.MaxEntries {
				return refuse(apierr.TooLarge, "the archive holds more entries than it may", "entries")
			}
			continue
		}
		e.Index = s.sum.Entries + 1
		if err := s.add(e); err != nil {
			return err
		}
	}
}

// entryOf makes the entry of a zip file's name and mode, after its name and kind are checked.
func entryOf(raw string, dir bool, mode fs.FileMode, size, packed int64) (Entry, error) {
	if mode&(fs.ModeSymlink|fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0 {
		return Entry{}, refuse(apierr.InvalidPath, "the archive holds a link or a device, which is not imported", "")
	}
	parts, err := cleanPath(raw, dir)
	if err != nil {
		return Entry{}, err
	}
	out := Entry{Dir: dir, Names: parts, Size: size, Packed: packed}
	if dir {
		out.Size, out.Packed = 0, 0
	}
	display, key := make([]string, len(parts)), make([]string, len(parts))
	for i, p := range parts {
		display[i], key[i] = p.Name, p.Key
	}
	out.Path, out.Key = strings.Join(display, "/"), strings.Join(key, "/")
	return out, nil
}

// entryOfTar makes the entry of a tar header. ok is false for a header that is not an entry (a global extended header).
func entryOfTar(h *tar.Header) (Entry, bool, error) {
	switch h.Typeflag {
	case tar.TypeXGlobalHeader:
		return Entry{}, false, nil
	case tar.TypeReg, '\x00':
		e, err := entryOf(h.Name, false, h.FileInfo().Mode(), h.Size, 0)
		return e, err == nil, err
	case tar.TypeDir:
		e, err := entryOf(h.Name, true, fs.ModeDir, 0, 0)
		return e, err == nil, err
	case tar.TypeSymlink, tar.TypeLink:
		return Entry{}, false, refuse(apierr.InvalidPath, "the archive holds a link, which is not imported", "")
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		return Entry{}, false, refuse(apierr.InvalidPath, "the archive holds a device or a pipe, which is not imported", "")
	}
	return Entry{}, false, refuse(apierr.InvalidPath, "the archive holds an entry of a kind that is not imported", "")
}

// cleanPath checks an entry's name and splits it into its segments, each named and keyed as a store name. The rules: no
// absolute path, no drive letter, no ".." or "." or empty segment, no backslash, NUL or control character. A folder's
// trailing slash is dropped. Every segment is normalised with names.Clean, so the clash key is the store's.
func cleanPath(raw string, dir bool) ([]Name, error) {
	s := raw
	if dir {
		s = strings.TrimSuffix(s, "/")
	}
	if s == "" {
		return nil, refuse(apierr.InvalidPath, "an entry has no name", "")
	}
	for _, c := range s {
		if c == 0 || c < 0x20 || c == 0x7f || c == '\\' {
			return nil, refuse(apierr.InvalidPath, "an entry's name holds a backslash or a control character", "")
		}
	}
	if strings.HasPrefix(s, "/") || (len(s) >= 2 && s[1] == ':') {
		return nil, refuse(apierr.InvalidPath, "an entry's name is an absolute path", "")
	}
	segs := strings.Split(s, "/")
	out := make([]Name, 0, len(segs))
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return nil, refuse(apierr.InvalidPath, "an entry's name has an empty, '.' or '..' segment", "")
		}
		name, key, err := names.Clean(seg)
		if err != nil {
			return nil, err
		}
		out = append(out, Name{Name: name, Key: key})
	}
	return out, nil
}

// openTar opens a tar stream of the given format, and returns it with the function that closes its decompression.
// The decompressed bytes of a gzip go through cr when it is not nil, so every byte of the stream counts, the headers of
// entries that are not imported too (decision 92).
func openTar(r io.ReaderAt, size int64, f Format, cr *countReader) (*tar.Reader, func(), error) {
	src := io.NewSectionReader(r, 0, size)
	if f == Tar {
		return tar.NewReader(src), func() {}, nil
	}
	gz, err := gzip.NewReader(bufio.NewReaderSize(src, 1<<16))
	if err != nil {
		return nil, nil, refuse(apierr.UnsupportedMedia, "the gzip stream is not valid", "")
	}
	var out io.Reader = gz
	if cr != nil {
		cr.r = gz
		out = cr
	}
	return tar.NewReader(out), func() { gz.Close() }, nil
}

// errOverLimit is what a read of a countReader returns past its limit.
var errOverLimit = errors.New("the archive expands more than it may")

// countReader counts the bytes read through it. Past its limit it sets over and fails the read, so a stream that expands
// far past the caps is stopped while it is read, not after.
type countReader struct {
	r     io.Reader
	n     int64
	limit int64
	over  bool
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.limit {
		c.over = true
		return n, errOverLimit
	}
	return n, err
}

// tarLimit is how many decompressed bytes of a tar stream the caps allow. A gzip may expand to the ratio of its size,
// and to the total of its files plus a header allowance per entry. A plain tar is not limited: its size is the archive's.
func tarLimit(size int64, caps Caps, f Format) int64 {
	if f != TarGz {
		return math.MaxInt64
	}
	limit := int64(math.MaxInt64)
	if caps.MaxRatio > 0 {
		base := max(size, ratioFloor)
		if base <= math.MaxInt64/caps.MaxRatio {
			limit = caps.MaxRatio * base
		}
	}
	allowance := int64(caps.MaxEntries)*1024 + 1<<20
	if caps.MaxTotal >= 0 && caps.MaxTotal <= math.MaxInt64-allowance {
		limit = min(limit, caps.MaxTotal+allowance)
	}
	return limit
}

// OverRatio reports whether size is more than ratio times the larger of packed and ratioFloor. It divides rather than
// multiplies, so a large packed size cannot overflow the product into a small one. A bundle and an import both use this
// one definition (design 2.3), so they refuse the same entries.
func OverRatio(size, packed, ratio int64) bool {
	if ratio <= 0 {
		return size > 0
	}
	p := max(packed, ratioFloor)
	q := size / ratio
	return q > p || (q == p && size%ratio != 0)
}

// zipSizes reads the declared sizes of a zip entry as signed numbers, which the caps are checked against. A size above
// MaxInt64 would read as a negative number and pass every cap, and a compressed size above the archive's own size is not
// a real entry. Both are refused (decision 92).
func zipSizes(f *zip.File, archive int64) (int64, int64, error) {
	if f.UncompressedSize64 > math.MaxInt64 || f.CompressedSize64 > math.MaxInt64 {
		return 0, 0, refuse(apierr.TooLarge, "an entry of the archive declares a size no archive can hold", "size")
	}
	if int64(f.CompressedSize64) > archive {
		return 0, 0, refuse(apierr.UnsupportedMedia, "the zip's structure is not valid", "packed")
	}
	return int64(f.UncompressedSize64), int64(f.CompressedSize64), nil
}

// Walk calls fn for each entry of the archive after the first skip entries, in archive order, with a reader of the
// entry's bytes for a file (nil for a folder). fn reads what it needs; Walk goes on after fn returns. The entries are
// those Scan counted, so Index matches the scan. The skip reads the entries before the cursor, and it checks ctx at every
// entry: a cancelled ctx returns ctx's error, and it is never reported as a refusal of the archive (round 10).
func Walk(ctx context.Context, r io.ReaderAt, size int64, f Format, skip int, fn func(e Entry, body io.Reader) error) error {
	if f == Zip {
		zr, err := zip.NewReader(r, size)
		if err != nil {
			return refuse(apierr.UnsupportedMedia, "the zip's structure is not valid", "")
		}
		i := 0
		for _, zf := range zr.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			usz, pck, err := zipSizes(zf, size)
			if err != nil {
				return err
			}
			e, err := entryOf(zf.Name, zf.FileInfo().IsDir(), zf.Mode(), usz, pck)
			if err != nil {
				return err
			}
			i++
			e.Index = i
			if i <= skip {
				continue
			}
			if e.Dir {
				if err := fn(e, nil); err != nil {
					return err
				}
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return refuse(apierr.UnsupportedMedia, "a file of the zip cannot be read", "")
			}
			err = fn(e, rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	tr, closeFn, err := openTar(r, size, f, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	i := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			return refuse(apierr.UnsupportedMedia, "the tar archive is not valid", "")
		}
		e, ok, err := entryOfTar(h)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		i++
		e.Index = i
		if i <= skip {
			continue
		}
		var body io.Reader
		if !e.Dir {
			body = tr
		}
		if err := fn(e, body); err != nil {
			return err
		}
	}
}
