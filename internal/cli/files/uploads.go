// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

// uploadView is one upload as upload ls --json prints it.
type uploadView struct {
	ID         string    `json:"id"`
	Server     string    `json:"server"`
	Profile    string    `json:"profile,omitempty"`
	Local      string    `json:"local"`
	Folder     string    `json:"folder"`
	Path       string    `json:"path,omitempty"`
	Parent     string    `json:"parent,omitempty"`
	Name       string    `json:"name"`
	OnConflict string    `json:"on_conflict"`
	Size       int64     `json:"size"`
	Sent       int64     `json:"sent"`
	StartedAt  time.Time `json:"started_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// UploadLs is `selfcloud upload ls [--json]`: the uploads this client began and has not finished, oldest first, from its
// own records. It calls no route: the server has no list of uploads, and the records say what put last heard (SENT is
// the offset the server last acknowledged, EXPIRES the end it last gave).
func UploadLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("upload ls")
	var asJSON bool
	set.BoolVar(&asJSON, "json", false, "print JSON, and errors as JSON on stderr")
	pos, err := conn.Parse(set, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, asJSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, asJSON, exitcode.Usagef("upload ls takes no arguments"))
	}
	recs, bad, err := listUploadRecords()
	if err != nil {
		return output.Fail(stderr, asJSON, err)
	}
	if bad > 0 {
		fmt.Fprintf(stderr, "selfcloud: note: %d upload record(s) could not be read and are not listed\n", bad)
	}
	if asJSON {
		views := make([]uploadView, 0, len(recs))
		for _, r := range recs {
			views = append(views, uploadView{ID: r.ID, Server: r.Server, Profile: r.Profile, Local: r.Local,
				Folder: r.Folder, Path: r.Path, Parent: r.Parent, Name: r.Name, OnConflict: r.OnConflict, Size: r.Size,
				Sent: r.Sent, StartedAt: r.StartedAt, ExpiresAt: r.ExpiresAt})
		}
		if err := output.JSON(stdout, views); err != nil {
			return output.Fail(stderr, true, err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(recs))
	for _, r := range recs {
		expires := ""
		if !r.ExpiresAt.IsZero() {
			expires = r.ExpiresAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{r.ID, strconv.FormatInt(r.Sent, 10), strconv.FormatInt(r.Size, 10), expires,
			r.Server, r.Local})
	}
	if err := output.Table(stdout, []string{"ID", "SENT", "SIZE", "EXPIRES", "SERVER", "LOCAL"}, rows); err != nil {
		return output.Fail(stderr, false, err)
	}
	return exitcode.OK
}

// UploadCancel is `selfcloud upload cancel ID`: it ends an upload on the server (tus termination), which drops the bytes
// it holds, and forgets the upload here. An upload the server says has expired is forgotten too, with exit 6.
func UploadCancel(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("upload cancel")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("upload cancel takes one upload id; selfcloud upload ls lists them"))
	}
	uplID := pos[0]
	if !id.Valid("upl", uplID) {
		return fail(stderr, g, exitcode.Usagef("an upload is named by its upl_ id; selfcloud upload ls lists them"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	_, err = c.Client.Do(context.Background(), client.Request{Method: http.MethodDelete, Path: uploadRoute(uplID, ""),
		Header: tusHeader(), Accept: "application/json"})
	if err != nil {
		if apiCode(err) == "upload_expired" {
			_ = forgetUpload(uplID)
			err = &exitcode.Error{Exit: exitcode.UploadExpired, Name: "upload_expired", Message: "the upload had " +
				"already expired on the server, which drops it by itself; its record here is removed"}
		}
		return report(stdout, stderr, g.JSON, []outcome{failedOutcome(uplID, err)})
	}
	if err := forgetUpload(uplID); err != nil {
		fmt.Fprintf(stderr, "selfcloud: note: the upload was ended, but its record could not be removed: %v\n", err)
	}
	return report(stdout, stderr, g.JSON, []outcome{doneOutcome(uplID)})
}
