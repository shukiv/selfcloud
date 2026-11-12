// SPDX-License-Identifier: AGPL-3.0-or-later

package files

// Safe writes for downloads (design-cli 3.9). A download's name is the server's, and the server's name is untrusted, so
// it is checked with the API's own name rules and then refused if it would not stay one plain file name on any
// platform. Every write goes through an os.Root of the destination directory, so no name can leave it, even through a
// link already there. The part file is created with O_EXCL and mode 0600; an existing one is reopened only when it is a
// regular file with one link, owned by this user, and only the file Lstat saw. A part file is never emptied in place:
// to start again it is removed and made anew. The finished file takes the mode a new file gets under the umask, and its
// name by a hard link, which fails when the name exists, or by a rename over a regular file with --overwrite. A
// symbolic link at either name is never followed or replaced.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/names"
)

const (
	partSuffix = ".part"
	// maxLocalName is the longest file name most file systems hold, in bytes.
	maxLocalName = 255
	// partMode keeps the bytes of a download, which may come from a private link, to their owner until they are checked.
	partMode = 0o600
	// nameHint is how a person names the destination instead of taking the server's name.
	nameHint = "give the destination a name of its own: selfcloud get FILE DIR/NAME"
)

// umaskStatusPath is where the process's umask is read, as /proc gives it. Tests point it elsewhere.
var umaskStatusPath = "/proc/self/status"

// newFileMode is the mode a new file gets: 0666 less the umask, read from /proc so the umask is not changed to learn it,
// or the part file's mode, 0600, when the umask cannot be read, so the finished file is never more open than the umask
// allows.
func newFileMode() os.FileMode {
	b, err := os.ReadFile(umaskStatusPath)
	if err != nil {
		return partMode
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Umask:"); ok {
			if m, err := strconv.ParseUint(strings.TrimSpace(v), 8, 32); err == nil {
				return 0o666 &^ os.FileMode(m&0o777)
			}
		}
	}
	return partMode
}

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
		return "", exitcode.Usagef("the file's name is not safe to write: it is a device name on some systems")
	}
	if len(clean)+len(partSuffix) > maxLocalName {
		return "", exitcode.Usagef("the file's name is too long to write beside its part file")
	}
	return clean, nil
}

// serverName checks the server's name for a file written under it, when the person named no destination file: the
// rules of localName, and no leading dot, since a hidden file, such as a shell's start-up file in the home directory, is
// not one a download makes unasked. Each refusal says how to name the destination.
func serverName(name string) (string, error) {
	clean, err := localName(name)
	if err != nil {
		return "", withHint(err, nameHint)
	}
	if strings.HasPrefix(clean, ".") {
		return "", exitcode.Usagef("the file's name on the server, %s, starts with a dot, so it would be a hidden "+
			"file; %s", clean, nameHint)
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

// partFile is a download's part file in its directory's root. fi is the file that was opened, so a name given the file
// while the bytes arrive is not acted on. sum is the SHA-256 of the bytes it holds.
type partFile struct {
	root *os.Root
	name string
	part string
	f    *os.File
	fi   fs.FileInfo
	size int64
	sum  hash.Hash
}

// newPart makes the part file, mode 0600. O_EXCL refuses any name that exists, a symbolic link included, so nothing is
// followed.
func newPart(root *os.Root, name string) (*partFile, error) {
	part := name + partSuffix
	f, err := createPart(root, part)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot read the part file: %w", err)
	}
	return &partFile{root: root, name: name, part: part, f: f, fi: fi, sum: sha256.New()}, nil
}

// createPart creates the part file with O_EXCL and mode 0600.
func createPart(root *os.Root, part string) (*os.File, error) {
	f, err := root.OpenFile(part, os.O_RDWR|os.O_CREATE|os.O_EXCL, partMode)
	if errors.Is(err, fs.ErrExist) {
		return nil, exitcode.Usagef("%s appeared at the destination; nothing was written", part)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot make the part file: %w", err)
	}
	return f, nil
}

// removePart removes a part file from a root. Tests make it fail, since a file system that refuses the removal of a file
// its owner can write to is hard to make on every machine.
var removePart = (*os.Root).Remove

// linkPart gives a part file a second name. Tests make it fail like a file system without hard links, since one is hard
// to make on every machine.
var linkPart = (*os.Root).Link

// replacePart removes a part file this download will not continue and makes a new one in its place, so the old one's
// bytes, and any other name it has, are left as they were. In a shared directory with the sticky bit, the removal of
// another user's file fails, and nothing is written: that is a usage failure (exit 2), not a failed write.
func replacePart(root *os.Root, name string) (*partFile, error) {
	part := name + partSuffix
	if err := removePart(root, part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if removeAllowed(err) == nil {
			return nil, &exitcode.Error{Exit: exitcode.Usage, Name: "usage", Message: "cannot remove " + part +
				" to start again: " + err.Error() + "; nothing was changed"}
		}
		return nil, fmt.Errorf("cannot remove %s to start again: %w", part, err)
	}
	return newPart(root, name)
}

// beforePartOpen runs between the look at a part file and its opening. Tests use it to put a link in its place.
var beforePartOpen = func(part string) {}

// oldPart reopens the part file to continue it. It must be a regular file with one link, owned by this user, and the
// file opened must be the one Lstat saw, so a link put in its place meanwhile is not followed. With hashBytes, the bytes
// it holds are hashed, so the check at the end covers the whole file.
func oldPart(root *os.Root, name string, hashBytes bool) (*partFile, error) {
	part := name + partSuffix
	fi, err := root.Lstat(part)
	if err != nil {
		return nil, fmt.Errorf("cannot read the part file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, exitcode.Usagef("%s is not a regular file, so it is not used", part)
	}
	if p := partProblem(fi); p != "" {
		return nil, exitcode.Usagef("%s %s, so it is not used", part, p)
	}
	beforePartOpen(part)
	f, err := root.OpenFile(part, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open the part file: %w", err)
	}
	st, err := f.Stat()
	if err != nil || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, exitcode.Usagef("%s changed while it was opened; nothing was written", part)
	}
	if p := partProblem(st); p != "" {
		_ = f.Close()
		return nil, exitcode.Usagef("%s %s, so it is not used", part, p)
	}
	pf := &partFile{root: root, name: name, part: part, f: f, fi: st, size: st.Size(), sum: sha256.New()}
	if hashBytes && pf.size > 0 {
		if _, err := io.Copy(pf.sum, io.NewSectionReader(f, 0, pf.size)); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("cannot read the part file: %w", err)
		}
	}
	return pf, nil
}

// reset starts the part file again. It is never emptied in place: it is closed, removed and made anew with O_EXCL, so
// whatever took its name meanwhile is refused rather than written.
func (pf *partFile) reset() error {
	_ = pf.f.Close()
	if !pf.stillAtName() {
		return pf.replacedError()
	}
	if err := pf.root.Remove(pf.part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &localWriteError{err}
	}
	f, err := createPart(pf.root, pf.part)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return &localWriteError{err}
	}
	pf.f, pf.fi, pf.size = f, fi, 0
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

// discard closes and removes the part file, unless another file has taken its name: that file is kept.
func (pf *partFile) discard() {
	_ = pf.f.Close()
	if pf.stillAtName() {
		_ = pf.root.Remove(pf.part)
	}
}

// stillAtName reports whether the file at the part file's name is still the one this download opened. It is checked
// before every Link, Rename and Remove, so none acts on a file that took the name meanwhile.
func (pf *partFile) stillAtName() bool {
	fi, err := pf.root.Lstat(pf.part)
	return err == nil && os.SameFile(pf.fi, fi)
}

// replacedError refuses a file at the part file's name that this download did not write. It is a failure (exit 1), as
// the bytes it was to write are gone.
func (pf *partFile) replacedError() error {
	return &exitcode.Error{Exit: exitcode.Failed, Name: "part_replaced", Message: fmt.Sprintf("the part file was "+
		"replaced during the download, so the file at %s is not the one this get checked; it is kept", pf.part)}
}

// finish gives the part file the mode a new file gets, syncs it and gives it its name. Without overwrite the name is
// taken by a hard link, which fails if the name exists, and the part file's name is removed; on a file system without
// hard links, on Linux a rename that never replaces a file (renameat2 with RENAME_NOREPLACE), and where the file system
// has none either, a refusal that keeps the part file; on other platforms a rename after a check, which leaves a short
// window. With overwrite, a rename over the regular file there.
func (pf *partFile) finish(overwrite bool) error {
	if err := pf.f.Chmod(newFileMode()); err != nil {
		return &localWriteError{err}
	}
	if err := pf.f.Sync(); err != nil {
		return &localWriteError{err}
	}
	if err := pf.f.Close(); err != nil {
		return &localWriteError{err}
	}
	if !pf.stillAtName() {
		return pf.replacedError()
	}
	if overwrite {
		if err := checkFinal(pf.root, pf.name, true); err != nil {
			return err
		}
		return pf.root.Rename(pf.part, pf.name)
	}
	err := linkPart(pf.root, pf.part, pf.name)
	if err == nil {
		if pf.stillAtName() {
			_ = pf.root.Remove(pf.part)
		}
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return pf.appearedError()
	}
	return pf.takeNameWithoutLinks()
}

// appearedError refuses a file that took the destination's name while the download was written. It is kept.
func (pf *partFile) appearedError() error {
	return exitcode.Usagef("%s appeared at the destination meanwhile; the download is kept as %s, and --overwrite "+
		"replaces it", pf.name, pf.part)
}
