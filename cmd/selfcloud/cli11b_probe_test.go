// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "strings"

// versionPair makes a file called name with two contents, the second replacing the first, and returns the file's node id
// and the id of the version that the replacement kept.
func (p *fileProbe) versionPair(name string) (nodeID, verID string) {
	p.t.Helper()
	nodeID = valueOf(p.t, p.do("put", p.local(name, "first "+name), "--folder", p.folder, "/"), "id=")
	p.do("put", p.local(name, "second "+name), "--folder", p.folder, "/", "--replace")
	return nodeID, p.oldVersion(nodeID)
}

// oldVersion returns the id of the only version of a file that is not its current content.
func (p *fileProbe) oldVersion(nodeID string) string {
	p.t.Helper()
	for _, line := range strings.Split(p.do("version", "ls", nodeID), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "ver_") {
			return fields[0]
		}
	}
	p.t.Fatalf("no version of %s in its list", nodeID)
	return ""
}
