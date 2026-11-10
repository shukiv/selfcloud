// SPDX-License-Identifier: AGPL-3.0-or-later

// Package info is `selfcloud info`: the server's public description, GET /api/v1/info. The route is public, so no
// credential is needed; one is sent only when the profile's own address is used.
package info

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// view is the part of the answer `info` prints as key=value lines. --json prints the whole document.
type view struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	PublicURL    string `json:"public_url"`
	WebInterface bool   `json:"web_interface"`
	Brand        struct {
		Name string `json:"name"`
	} `json:"brand"`
	PasswordMinLength int `json:"password_min_length"`
	Limits            struct {
		MaxFileSize  int64 `json:"max_file_size"`
		MaxChunkSize int64 `json:"max_chunk_size"`
	} `json:"limits"`
	SharingMaxExpiryDays int      `json:"sharing_max_expiry_days"`
	Features             []string `json:"features"`
}

// Run is `selfcloud info [flags]`.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := conn.NewFlagSet("info")
	var g conn.Global
	g.Register(fs)
	pos, err := conn.Parse(fs, args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitcode.OK
		}
		return output.Fail(stderr, g.JSON, err)
	}
	if len(pos) != 0 {
		return output.Fail(stderr, g.JSON, exitcode.Usagef("info takes no arguments"))
	}
	c, err := conn.Connect(g, stderr, false)
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	resp, err := c.Client.Do(context.Background(), client.Request{
		Method: "GET",
		Path:   "/api/v1/info",
		Accept: "application/json",
	})
	if err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	if g.JSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, resp.Body, "", "  "); err != nil {
			return output.Fail(stderr, g.JSON, errors.New("the server's answer is not JSON"))
		}
		buf.WriteByte('\n')
		if _, err := stdout.Write(buf.Bytes()); err != nil {
			return output.Fail(stderr, g.JSON, err)
		}
		return exitcode.OK
	}
	var v view
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		return output.Fail(stderr, g.JSON, errors.New("the server's answer is not the info document"))
	}
	pairs := []output.KV{
		{Key: "name", Value: v.Name},
		{Key: "version", Value: v.Version},
		{Key: "public_url", Value: v.PublicURL},
		{Key: "web_interface", Value: boolWord(v.WebInterface)},
		{Key: "password_min_length", Value: strconv.Itoa(v.PasswordMinLength)},
		{Key: "max_file_size", Value: strconv.FormatInt(v.Limits.MaxFileSize, 10)},
		{Key: "max_chunk_size", Value: strconv.FormatInt(v.Limits.MaxChunkSize, 10)},
		{Key: "sharing_max_expiry_days", Value: strconv.Itoa(v.SharingMaxExpiryDays)},
		{Key: "features", Value: strings.Join(v.Features, ",")},
		{Key: "brand_name", Value: v.Brand.Name},
	}
	if err := output.KeyValues(stdout, pairs); err != nil {
		return output.Fail(stderr, g.JSON, err)
	}
	return exitcode.OK
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
