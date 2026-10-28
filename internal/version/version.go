// SPDX-License-Identifier: AGPL-3.0-or-later

// Package version holds the build version.
package version

// Version is set at link time:
//
//	go build -ldflags "-X selfcloud/internal/version.Version=1.2.3"
var Version = "dev"
