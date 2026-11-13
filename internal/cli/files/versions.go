// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/id"
)

// versionCurrentID is the list entry that holds the file's current content. It is not a version that can be read or
// changed: the content route of the node is.
const versionCurrentID = "current"

// versionMaxLabelRunes is the longest label the API takes, counted in characters.
const versionMaxLabelRunes = 100

// versionView is one version as the API writes it. --json prints it again, so it holds exactly the API's fields.
type versionView struct {
	ID          string  `json:"id"`
	Current     bool    `json:"current"`
	Size        int64   `json:"size"`
	MimeType    string  `json:"mime_type,omitempty"`
	ETag        string  `json:"etag"`
	SHA256      string  `json:"sha256,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CreatedBy   string  `json:"created_by,omitempty"`
	ReplacedAt  *string `json:"replaced_at,omitempty"`
	ReplacedBy  string  `json:"replaced_by,omitempty"`
	Label       *string `json:"label"`
	KeepForever bool    `json:"keep_forever"`
}

// versionChange is the body of a label change: only the fields the person gave.
type versionChange struct {
	Label       *string `json:"label,omitempty"`
	KeepForever *bool   `json:"keep_forever,omitempty"`
}

// versionListRoute is a file's versions.
func versionListRoute(nodeID string) string { return nodeRoute(nodeID, "/versions") }

// versionRoute is one version of a file.
func versionRoute(nodeID, verID string) string { return versionListRoute(nodeID) + "/" + verID }

// versionContentRoute is one version's bytes.
func versionContentRoute(nodeID, verID string) string {
	return versionRoute(nodeID, verID) + "/content"
}

// versionArg checks a version id. The current content is not a version, since it is read from the file's own content
// route.
func versionArg(arg string) (string, error) {
	if arg == versionCurrentID {
		return "", exitcode.Usagef("the current content is not a version; use get for it")
	}
	if !id.Valid("ver", arg) {
		return "", exitcode.Usagef("a version is named by its ver_ id")
	}
	return arg, nil
}

// versionItems reads a file's versions, the current content first.
func versionItems(c *conn.Conn, nodeID string) ([]versionView, error) {
	resp, err := send(c, request{method: http.MethodGet, path: versionListRoute(nodeID)})
	if err != nil {
		return nil, err
	}
	var doc struct {
		Items []versionView `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return nil, errors.New("the server's answer is not a list of versions")
	}
	if doc.Items == nil {
		doc.Items = []versionView{}
	}
	return doc.Items, nil
}

// versionFind finds one version in the list, or reports that the file has none by that id.
func versionFind(items []versionView, verID string) (versionView, error) {
	for _, v := range items {
		if v.ID == verID {
			return v, nil
		}
	}
	return versionView{}, exitcode.NotFoundf("no such version")
}

// versionBytes is the version a download reads, refusing one without a valid ETag, size or SHA-256.
func versionBytes(v versionView) (version, error) {
	if !validETag(v.ETag) || v.Size < 0 {
		return version{}, errors.New("the server gives the version no valid version (etag) or size")
	}
	ver := version{etag: v.ETag, size: v.Size}
	if v.SHA256 != "" {
		b, err := hex.DecodeString(v.SHA256)
		if err != nil || len(b) != 32 {
			return version{}, errors.New("the server's SHA-256 of the version is not valid")
		}
		ver.sha = hex.EncodeToString(b)
	}
	return ver, nil
}

// versionReplaced is the time a version was replaced, or "" for the current content.
func versionReplaced(v versionView) string {
	if v.ReplacedAt == nil {
		return ""
	}
	return *v.ReplacedAt
}

// versionLabelText is a version's label, or "" when it has none.
func versionLabelText(v versionView) string {
	if v.Label == nil {
		return ""
	}
	return *v.Label
}

// versionKeepText says whether the file keeps a version for good.
func versionKeepText(v versionView) string {
	if v.KeepForever {
		return "yes"
	}
	return ""
}

// versionPrint writes one version: key=value lines, or the view under --json.
func versionPrint(stdout, stderr io.Writer, g conn.Global, v versionView) int {
	var err error
	if g.JSON {
		err = output.JSON(stdout, v)
	} else {
		err = output.KeyValues(stdout, []output.KV{
			{Key: "id", Value: v.ID},
			{Key: "current", Value: strconv.FormatBool(v.Current)},
			{Key: "size", Value: strconv.FormatInt(v.Size, 10)},
			{Key: "mime_type", Value: v.MimeType},
			{Key: "etag", Value: v.ETag},
			{Key: "sha256", Value: v.SHA256},
			{Key: "created_at", Value: v.CreatedAt},
			{Key: "created_by", Value: v.CreatedBy},
			{Key: "replaced_at", Value: versionReplaced(v)},
			{Key: "replaced_by", Value: v.ReplacedBy},
			{Key: "label", Value: versionLabelText(v)},
			{Key: "keep_forever", Value: strconv.FormatBool(v.KeepForever)},
		})
	}
	if err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// VersionLs is `selfcloud version ls NODE`: a file's versions, the current content first.
func VersionLs(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("version ls")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return fail(stderr, g, exitcode.Usagef("version ls takes one file, named by its nod_ id"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, err := versionItems(c, nodeID)
	if err != nil {
		return fail(stderr, g, err)
	}
	if g.JSON {
		if err := output.JSON(stdout, items); err != nil {
			return fail(stderr, g, err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(items))
	for _, v := range items {
		rows = append(rows, []string{v.ID, strconv.FormatInt(v.Size, 10), v.CreatedAt, versionReplaced(v),
			versionLabelText(v), versionKeepText(v)})
	}
	if err := output.Table(stdout, []string{"ID", "SIZE", "CREATED", "REPLACED", "LABEL", "KEEP"}, rows); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// VersionGet is `selfcloud version get NODE VER [DEST]`: a resumable download of one version, with get's destination
// and transfer rules.
func VersionGet(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("version get")
	var g conn.Global
	g.Register(set)
	var tf transferFlags
	tf.register(set)
	var overwrite bool
	set.BoolVar(&overwrite, "overwrite", false, "replace a regular file at the destination, once the download is "+
		"complete and checked; a part file this download cannot continue is replaced by a new one")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) < 2 || len(pos) > 3 {
		return fail(stderr, g, exitcode.Usagef("version get takes a file, a version and a destination"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	verID, err := versionArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	piece, _, err := tf.pieceSize()
	if err != nil {
		return fail(stderr, g, err)
	}
	dest := ""
	if len(pos) == 3 {
		dest = pos[2]
	}
	dst, err := openDestination(dest, overwrite)
	if err != nil {
		return fail(stderr, g, err)
	}
	defer dst.close()
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	items, err := versionItems(c, nodeID)
	if err != nil {
		return fail(stderr, g, err)
	}
	view, err := versionFind(items, verID)
	if err != nil {
		return fail(stderr, g, err)
	}
	ver, err := versionBytes(view)
	if err != nil {
		return fail(stderr, g, err)
	}
	name := ""
	if dst.name == "" {
		// The file's current name is the one a version keeps, as get takes it.
		nv, err := statOf(context.Background(), c.Client, nodeID)
		if err != nil {
			return fail(stderr, g, err)
		}
		name = nv.Name
	}
	subj := subject{key: nodeID, route: versionContentRoute(nodeID, verID), name: name, verb: "version get", ver: ver,
		restat: func(context.Context) (version, error) {
			items, err := versionItems(c, nodeID)
			if err != nil {
				return version{}, err
			}
			view, err := versionFind(items, verID)
			if err != nil {
				return version{}, err
			}
			return versionBytes(view)
		}}
	got, err := downloadSubject(context.Background(), signedIn{c.Client}, subj, dst,
		dlOptions{piece: piece, stall: tf.stall, overwrite: overwrite}, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	return printGot(stdout, stderr, g, got)
}

// VersionLabel is `selfcloud version label NODE VER [--label TEXT] [--keep|--no-keep]`: it changes only what was
// given, and an empty label removes the label.
func VersionLabel(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("version label")
	var g conn.Global
	g.Register(set)
	var label string
	var keep, noKeep bool
	set.StringVar(&label, "label", "", "set the version's `text` label, at most 100 characters; \"\" removes it")
	set.BoolVar(&keep, "keep", false, "keep the version for good")
	set.BoolVar(&noKeep, "no-keep", false, "let the version go when the file keeps too many (the default)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("version label takes a file and a version"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	verID, err := versionArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["label"] && !given["keep"] && !given["no-keep"] {
		return fail(stderr, g, exitcode.Usagef("version label takes --label, --keep or --no-keep"))
	}
	if given["keep"] && given["no-keep"] {
		return fail(stderr, g, exitcode.Usagef("--keep and --no-keep say opposite things; give one"))
	}
	if given["label"] && utf8.RuneCountInString(label) > versionMaxLabelRunes {
		return fail(stderr, g, exitcode.Usagef("a label is at most %d characters", versionMaxLabelRunes))
	}
	change := versionChange{}
	if given["label"] {
		change.Label = &label
	}
	if given["keep"] || given["no-keep"] {
		v := keep
		change.KeepForever = &v
	}
	body, err := json.Marshal(change)
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	resp, err := send(c, request{method: http.MethodPatch, path: versionRoute(nodeID, verID), body: body,
		contentType: "application/json"})
	if err != nil {
		return fail(stderr, g, err)
	}
	var view versionView
	if err := json.Unmarshal(resp.Body, &view); err != nil {
		return fail(stderr, g, errors.New("the server's answer is not a version"))
	}
	return versionPrint(stdout, stderr, g, view)
}

// VersionRestore is `selfcloud version restore NODE VER`: the version becomes the file's current content, and the
// content it replaces is kept as a version. It asks nothing, since nothing is lost.
func VersionRestore(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("version restore")
	var g conn.Global
	g.Register(set)
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("version restore takes a file and a version"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	verID, err := versionArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	nv, err := statOf(context.Background(), c.Client, nodeID)
	if err != nil {
		return fail(stderr, g, err)
	}
	if !validETag(nv.ETag) {
		return fail(stderr, g, errors.New("the server gives the file no valid version (etag)"))
	}
	resp, err := c.Client.Do(context.Background(), client.Request{Method: http.MethodPost,
		Path: versionRoute(nodeID, verID) + "/restore", Accept: "application/json",
		Header: http.Header{"If-Match": {nv.ETag}}})
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := printNode(stdout, resp.Body, g.JSON); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// VersionRm is `selfcloud version rm NODE VER [--yes]`: it removes one version, asking first unless --yes is given.
func VersionRm(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("version rm")
	var g conn.Global
	g.Register(set)
	var yes bool
	set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return fail(stderr, g, exitcode.Usagef("version rm takes a file and a version"))
	}
	nodeID, err := nodeArg(pos[0])
	if err != nil {
		return fail(stderr, g, err)
	}
	verID, err := versionArg(pos[1])
	if err != nil {
		return fail(stderr, g, err)
	}
	if err := confirm(yes, verID, "remove the version from the server", stderr); err != nil {
		return fail(stderr, g, err)
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}
	if _, err := send(c, request{method: http.MethodDelete, path: versionRoute(nodeID, verID)}); err != nil {
		return fail(stderr, g, err)
	}
	var err2 error
	if g.JSON {
		err2 = output.JSON(stdout, map[string]string{"deleted": verID})
	} else {
		err2 = output.KeyValues(stdout, []output.KV{{Key: "deleted", Value: verID}})
	}
	if err2 != nil {
		return fail(stderr, g, err2)
	}
	return exitcode.OK
}
