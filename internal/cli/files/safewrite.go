// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// Safe writes for downloads (design-cli 3.9). A download's name is the server's, and the server's name is untrusted, so
// it is checked with the API's own name rules and then refused if it would not stay one plain file name on any
// platform. Every write goes through an os.Root of the destination directory, so no name can leave it, even through a
// link already there. The part file is created with O_EXCL; an existing one is reopened only when it is a regular file,
// and only the file Lstat saw. The finished file takes its name by a hard link, which fails when the name exists, or by
// a rename over a regular file with --overwrite. A symbolic link at either name is never followed or replaced.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/names"
)

const (
	partSuffix = ".part"
	// maxLocalName is the longest file name most file systems hold, in bytes.
	maxLocalName = 255
)

// reservedNames are names that some systems open as a device in any directory, whatever their extension. They are
// refused on every platform, so the rule does not depend on the build.
var reservedNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

func init() {
	for _, d := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
		reservedNames["COM"+d] = true
		reservedNames["LPT"+d] = true
	}
}

// localName checks a name for a file on the local disk and returns it in the form the API stores (NFC). It refuses what
// the API's rules refuse (an empty name, . or .., a separator, a NUL or another control character, an invisible
// character, a space at either end), a name that is not one path element, a reserved device name, and a name too long
// to take .part. The refusal does not repeat the name.
func localName(name string) (string, error) {
	clean, _, err := names.Clean(name)
	if err != nil {
		return "", exitcode.Usagef("the file's name is not safe to write: %s", err)
	}
	if filepath.Base(clean) != clean || strings.ContainsAny(clean, `/\`) {
		return "", exitcode.Usagef("the file's name is not safe to write: it holds a path separator")
	}
	stem, _, _ := strings.Cut(clean, ".")
	if reservedNames[strings.ToUpper(strings.TrimRight(stem, " "))] {
		return "", exitcode.Usagef("the file's name is not safe to write: it is a device name on some systems; " +
			"give the destination a name of its own")
	}
	if len(clean)+len(partSuffix) > maxLocalName {
		return "", exitcode.Usagef("the file's name is too long to write beside its part file; give the destination " +
			"a shorter name")
	}
	return clean, nil
}

// target is where a download goes: a directory that exists, and a name in it ("" for the server's name).
type target struct {
	dir  string
	name string
}

// resolveTarget reads the destination: none is the current directory; an existing directory, or a path that ends in a
// separator, takes the file's own name; any other path names the file, in a directory that exists.
func resolveTarget(arg string) target {
	if arg == "" {
		return target{dir: "."}
	}
	if strings.HasSuffix(arg, string(filepath.Separator)) || strings.HasSuffix(arg, "/") {
		return target{dir: arg}
	}
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		return target{dir: arg}
	}
	return target{dir: filepath.Dir(arg), name: filepath.Base(arg)}
}

// checkFinal refuses a destination name that is taken, unless overwrite is set and it is a regular file.
func checkFinal(root *os.Root, name string, overwrite bool) error {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("cannot read the destination: %w", err)
	case !fi.Mode().IsRegular():
		return exitcode.Usagef("%s is at the destination and is not a regular file, so it is not replaced", name)
	case !overwrite:
		return exitcode.Usagef("%s exists at the destination; pass --overwrite to replace it", name)
	}
	return nil
}

// partFile is a download's part file in its directory's root. sum is the SHA-256 of the bytes it holds.
type partFile struct {
	root *os.Root
	name string
	part string
	f    *os.File
	size int64
	sum  hash.Hash
}

// newPart makes the part file. O_EXCL refuses any name that exists, a symbolic link included, so nothing is followed.
func newPart(root *os.Root, name string) (*partFile, error) {
	part := name + partSuffix
	f, err := root.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return nil, exitcode.Usagef("%s appeared at the destination; nothing was written", part)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot make the part file: %w", err)
	}
	return &partFile{root: root, name: name, part: part, f: f, sum: sha256.New()}, nil
}

// oldPart reopens the part file to continue it. It must be a regular file, and the file opened must be the one Lstat
// saw, so a link put in its place meanwhile is not followed. With hashBytes, the bytes it holds are hashed, so the
// check at the end covers the whole file.
func oldPart(root *os.Root, name string, hashBytes bool) (*partFile, error) {
	part := name + partSuffix
	fi, err := root.Lstat(part)
	if err != nil {
		return nil, fmt.Errorf("cannot read the part file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, exitcode.Usagef("%s is not a regular file, so it is not used", part)
	}
	f, err := root.OpenFile(part, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open the part file: %w", err)
	}
	st, err := f.Stat()
	if err != nil || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, exitcode.Usagef("%s changed while it was opened; nothing was written", part)
	}
	pf := &partFile{root: root, name: name, part: part, f: f, size: st.Size(), sum: sha256.New()}
	if hashBytes && pf.size > 0 {
		if _, err := io.Copy(pf.sum, io.NewSectionReader(f, 0, pf.size)); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("cannot read the part file: %w", err)
		}
	}
	return pf, nil
}

// reset empties the part file, to start again.
func (pf *partFile) reset() error {
	if err := pf.f.Truncate(0); err != nil {
		return &localWriteError{err}
	}
	pf.size = 0
	pf.sum.Reset()
	return nil
}

// localWriteError is a failure to write the local disk. It is never repeated, unlike a failure of the network.
type localWriteError struct{ err error }

func (e *localWriteError) Error() string { return "cannot write the part file: " + e.err.Error() }
func (e *localWriteError) Unwrap() error { return e.err }

// copyRange appends want bytes from r to the part file, hashing exactly the bytes written. A body that ends first is a
// transport failure, which may be repeated from the new size; a failed write is not.
func (pf *partFile) copyRange(r io.Reader, want int64) (int64, error) {
	buf := make([]byte, 256<<10)
	var done int64
	for done < want {
		chunk := buf[:min(int64(len(buf)), want-done)]
		n, rerr := r.Read(chunk)
		if n > 0 {
			w, werr := pf.f.WriteAt(chunk[:n], pf.size)
			pf.sum.Write(chunk[:w])
			pf.size += int64(w)
			done += int64(w)
			if werr != nil {
				return done, &localWriteError{werr}
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) && done < want {
				return done, &client.TransportError{Kind: client.KindFailed, Message: "the answer ended before its range did"}
			}
			if !errors.Is(rerr, io.EOF) {
				return done, rerr
			}
		}
	}
	return done, nil
}

// close closes the part file and keeps it, for a later get.
func (pf *partFile) close() { _ = pf.f.Close() }

// discard closes and removes the part file.
func (pf *partFile) discard() {
	_ = pf.f.Close()
	_ = pf.root.Remove(pf.part)
}

// finish syncs the part file and gives it its name. Without overwrite the name is taken by a hard link, which fails if
// the name exists, and the part file's name is removed; on a file system without hard links, a rename after a check.
// With overwrite, a rename over the regular file there.
func (pf *partFile) finish(overwrite bool) error {
	if err := pf.f.Sync(); err != nil {
		return &localWriteError{err}
	}
	if err := pf.f.Close(); err != nil {
		return &localWriteError{err}
	}
	if overwrite {
		if err := checkFinal(pf.root, pf.name, true); err != nil {
			return err
		}
		return pf.root.Rename(pf.part, pf.name)
	}
	err := pf.root.Link(pf.part, pf.name)
	if err == nil {
		_ = pf.root.Remove(pf.part)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return exitcode.Usagef("%s appeared at the destination meanwhile; the download is kept as %s, and --overwrite "+
			"replaces it", pf.name, pf.part)
	}
	if err := checkFinal(pf.root, pf.name, false); err != nil {
		return err
	}
	return pf.root.Rename(pf.part, pf.name)
}
