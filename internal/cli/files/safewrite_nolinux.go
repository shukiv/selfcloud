// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package files

// takeNameWithoutLinks gives a finished part file its name on a file system with no hard links: a check that the name is
// free, then a rename. A file that appears between the two is replaced; only Linux has a rename that cannot.
func (pf *partFile) takeNameWithoutLinks() error {
	if err := checkFinal(pf.root, pf.name, false); err != nil {
		return err
	}
	if !pf.stillAtName() {
		return pf.replacedError()
	}
	return pf.root.Rename(pf.part, pf.name)
}
