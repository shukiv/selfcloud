// SPDX-License-Identifier: AGPL-3.0-or-later

// Package manage is the command-line client's folder and group commands (design-cli.md section 8, slice cli8):
// folder create|ls|show|rename|rm, folder settings set, folder hide, folder show-again, group create|ls|show|rename|rm|
// members|add-member|remove-member, and user grants.
//
// The commands of an account (folder create, ls --account, show, rename, rm; every group command; user grants) name the
// account with --account acc_..., or the SELFCLOUD_ACCOUNT variable, and refuse anything that is not an account id before
// a request. The commands on a person's own folders (folder ls --mine, settings set, hide, show-again) name no account:
// the credential's own is used. A folder, a group and a user are named by their ids. A name is whatever the API accepts;
// one that starts with a dash is given after "--", as in: selfcloud folder create --account acc_... -- -name.
package manage

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/cli/profile"
)

// folderView is the part of a folder document that the plain output prints. --json prints the document as the API wrote it.
type folderView struct {
	ID                   string  `json:"id"`
	ExternalID           *string `json:"external_id"`
	Name                 string  `json:"name"`
	Kind                 string  `json:"kind"`
	RootNodeID           string  `json:"root_node_id"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	MembersOnly          bool    `json:"members_only"`
	SharingManagersOnly  bool    `json:"sharing_managers_only"`
	NoDownloadForViewers bool    `json:"no_download_for_viewers"`
	AllowLinks           bool    `json:"allow_links"`
	Role                 string  `json:"role"`
	Hidden               bool    `json:"hidden"`
}

func folderPairs(body []byte) ([]output.KV, error) {
	var v folderView
	if err := decodeAs(body, &v, "a folder"); err != nil {
		return nil, err
	}
	ext := ""
	if v.ExternalID != nil {
		ext = *v.ExternalID
	}
	return []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "external_id", Value: ext},
		{Key: "name", Value: v.Name},
		{Key: "kind", Value: v.Kind},
		{Key: "root_node_id", Value: v.RootNodeID},
		{Key: "members_only", Value: strconv.FormatBool(v.MembersOnly)},
		{Key: "sharing_managers_only", Value: strconv.FormatBool(v.SharingManagersOnly)},
		{Key: "no_download_for_viewers", Value: strconv.FormatBool(v.NoDownloadForViewers)},
		{Key: "allow_links", Value: strconv.FormatBool(v.AllowLinks)},
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
	}, nil
}

func accountFoldersRoute(acc string) string { return "/api/v1/accounts/" + acc + "/folders" }

// oneFolder is the shared start of the commands that take one folder id after the account.
func oneFolder(c *command, name string, args []string) (acc, folder string, code int, ok bool) {
	pos, code, ok := c.parse(args)
	if !ok {
		return "", "", code, false
	}
	if len(pos) != 1 {
		return "", "", c.fail(exitcode.Usagef("%s takes one folder id", name)), false
	}
	var err error
	if folder, err = idArg("fld", "folder", pos[0]); err != nil {
		return "", "", c.fail(err), false
	}
	if acc, err = c.accountID(); err != nil {
		return "", "", c.fail(err), false
	}
	return acc, folder, 0, true
}

// FolderCreate is `selfcloud folder create --account ACC [--external-id ID] NAME`: a team space.
func FolderCreate(args []string, stdout, stderr io.Writer) int {
	c := begin("folder create", true, stdout, stderr)
	var ext string
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the folder, kept with it")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("folder create takes one name"))
	}
	name, err := nameArg(pos[0])
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	body := map[string]string{"name": name}
	if ext != "" {
		body["external_id"] = ext
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, accountFoldersRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, folderPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// FolderLs is `selfcloud folder ls`: the team spaces of an account (--account), or the folders the credential reaches
// (--mine).
func FolderLs(args []string, stdout, stderr io.Writer) int {
	c := begin("folder ls", true, stdout, stderr)
	var mine, hidden, all bool
	var ext string
	c.set.BoolVar(&mine, "mine", false, "the folders this credential reaches, not an account's team spaces")
	c.set.BoolVar(&hidden, "hidden", false, "with --mine: only the spaces hidden from the list")
	c.set.StringVar(&ext, "external-id", "", "with --account: only the folder with this external `id`")
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("folder ls", pos); err != nil {
		return c.fail(err)
	}
	q := url.Values{}
	var path string
	switch {
	case mine:
		if c.account != "" {
			return c.fail(exitcode.Usagef("--mine and --account are two listings; give one"))
		}
		if ext != "" {
			return c.fail(exitcode.Usagef("--external-id is for the listing of an account"))
		}
		path = "/api/v1/folders"
		if hidden {
			q.Set("hidden", "true")
		}
	default:
		if hidden {
			return c.fail(exitcode.Usagef("--hidden is for --mine"))
		}
		acc, err := c.accountID()
		if err != nil {
			return c.fail(err)
		}
		path = accountFoldersRoute(acc)
		if ext != "" {
			q.Set("external_id", ext)
		}
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, path, q, all)
	if err != nil {
		return c.fail(err)
	}
	headers := []string{"ID", "NAME", "KIND"}
	if mine {
		headers = append(headers, "ROLE", "HIDDEN")
	}
	err = printList(c, items, next, headers, func(raw []byte) ([]string, error) {
		var v folderView
		if err := decodeAs(raw, &v, "a folder"); err != nil {
			return nil, err
		}
		row := []string{v.ID, v.Name, v.Kind}
		if mine {
			row = append(row, v.Role, strconv.FormatBool(v.Hidden))
		}
		return row, nil
	})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// FolderShow is `selfcloud folder show --account ACC FOLDER`.
func FolderShow(args []string, stdout, stderr io.Writer) int {
	c := begin("folder show", true, stdout, stderr)
	acc, folder, code, ok := oneFolder(c, "folder show", args)
	if !ok {
		return code
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, accountFoldersRoute(acc)+"/"+folder, nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, folderPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// FolderRename is `selfcloud folder rename --account ACC FOLDER NAME`.
func FolderRename(args []string, stdout, stderr io.Writer) int {
	c := begin("folder rename", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return c.fail(exitcode.Usagef("folder rename takes a folder id and a new name"))
	}
	folder, err := idArg("fld", "folder", pos[0])
	if err != nil {
		return c.fail(err)
	}
	name, err := nameArg(pos[1])
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPatch, accountFoldersRoute(acc)+"/"+folder, nil, map[string]string{"name": name})
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, folderPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// FolderRm is `selfcloud folder rm --account ACC FOLDER [--yes]`: removes a team space with its files. Asks first.
func FolderRm(args []string, stdout, stderr io.Writer) int {
	c := begin("folder rm", true, stdout, stderr)
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	acc, folder, code, ok := oneFolder(c, "folder rm", args)
	if !ok {
		return code
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	route := accountFoldersRoute(acc) + "/" + folder
	// Without a terminal and without --yes the command refuses before anything is read or sent. At a terminal the person
	// types the folder's name, which is read first.
	phrase := ""
	if !yes && files.IsTerminal(files.Stdin) {
		resp, err := files.Send(cn, http.MethodGet, route, nil, nil)
		if err != nil {
			return c.fail(err)
		}
		var v folderView
		if err := decodeAs(resp.Body, &v, "a folder"); err != nil {
			return c.fail(err)
		}
		phrase = v.Name
	}
	if err := files.Confirm(yes, phrase, "remove the folder, its files and its permissions for good", stderr); err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, route, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, folder); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// triState is a boolean flag that knows whether it was given.
type triState struct {
	set bool
	val bool
}

func (t *triState) String() string {
	if t == nil || !t.set {
		return ""
	}
	return strconv.FormatBool(t.val)
}

func (t *triState) Set(s string) error {
	switch s {
	case "true":
		t.val = true
	case "false":
		t.val = false
	default:
		return errors.New("true or false")
	}
	t.set = true
	return nil
}

// IsBoolFlag lets the bare flag mean true.
func (t *triState) IsBoolFlag() bool { return true }

var _ flag.Value = (*triState)(nil)

// FolderSettingsSet is `selfcloud folder settings set FOLDER [--members-only=B] ...`: a team space's restrictions.
func FolderSettingsSet(args []string, stdout, stderr io.Writer) int {
	c := begin("folder settings set", false, stdout, stderr)
	settings := []struct {
		flagName, key, help string
		v                   triState
	}{
		{flagName: "members-only", key: "members_only", help: "only the people given access reach the space"},
		{flagName: "sharing-managers-only", key: "sharing_managers_only", help: "only managers share what is in the space"},
		{flagName: "no-download-for-viewers", key: "no_download_for_viewers", help: "viewers may not download"},
		{flagName: "allow-links", key: "allow_links", help: "links to what is in the space may be made"},
	}
	for i := range settings {
		c.set.Var(&settings[i].v, settings[i].flagName, settings[i].help+" (true or false)")
	}
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("folder settings set takes one folder id"))
	}
	folder, err := idArg("fld", "folder", pos[0])
	if err != nil {
		return c.fail(err)
	}
	body := map[string]bool{}
	for i := range settings {
		if settings[i].v.set {
			body[settings[i].key] = settings[i].v.val
		}
	}
	if len(body) == 0 {
		return c.fail(exitcode.Usagef("give at least one setting, such as --allow-links=false"))
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPatch, "/api/v1/folders/"+folder+"/settings", nil, body)
	if err != nil {
		code := c.fail(err)
		// Every token is refused a space's settings. The refusal is the server's; the hint says where it is done.
		var ae *client.APIError
		if !c.g.JSON && cn.Kind == profile.KindDevice && errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			_, _ = io.WriteString(stderr, "selfcloud: hint: the web interface changes a space's settings\n")
		}
		return code
	}
	if err := printDocument(c, resp.Body, folderPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// hide is FolderHide and FolderShowAgain: they differ in the method only.
func hide(name, method string, args []string, stdout, stderr io.Writer) int {
	c := begin(name, false, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("%s takes one folder id", name))
	}
	folder, err := idArg("fld", "folder", pos[0])
	if err != nil {
		return c.fail(err)
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, method, "/api/v1/folders/"+folder+"/hidden", nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, folder); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// FolderHide is `selfcloud folder hide FOLDER`: takes a team space off the caller's own list. Access is unchanged.
func FolderHide(args []string, stdout, stderr io.Writer) int {
	return hide("folder hide", http.MethodPut, args, stdout, stderr)
}

// FolderShowAgain is `selfcloud folder show-again FOLDER`: puts a hidden team space back on the caller's list.
func FolderShowAgain(args []string, stdout, stderr io.Writer) int {
	return hide("folder show-again", http.MethodDelete, args, stdout, stderr)
}
