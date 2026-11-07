// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/fsck"
)

// fsckCommand checks the data directory against the database. It is read-only unless --repair is given, and then it
// only renames orphan blobs into quarantine. Exit 0 is clean, 1 means findings, 2 means the check could not run.
func fsckCommand(args []string, stdout, stderr io.Writer) int {
	var (
		asJSON, repair, verify bool
		minAge                 time.Duration
	)
	cfg, _, code := loadConfig("fsck", args, stderr, 0, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print one JSON document instead of the text summary")
		fs.BoolVar(&repair, "repair", false, "move orphan blobs to quarantine/<UTC date> (a rename; nothing is deleted)")
		fs.BoolVar(&verify, "verify-content", false, "hash every file against its row (reads all the contents; slow)")
		fs.DurationVar(&minAge, "min-age", time.Hour, "judge only files last modified at least this long before the check")
	})
	if cfg == nil {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rep, err := fsck.Run(ctx, fsck.Options{DataDir: cfg.DataDir, DBPath: cfg.Store.Path, MinAge: minAge,
		VerifyContent: verify, Repair: repair})
	if rep != nil {
		if asJSON {
			if werr := rep.WriteJSON(stdout); werr != nil {
				fmt.Fprintln(stderr, "selfcloud fsck:", werr)
				return 2
			}
		} else {
			rep.WriteText(stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud fsck:", err)
		return 2
	}
	if len(rep.Findings) > 0 {
		return 1
	}
	return 0
}
