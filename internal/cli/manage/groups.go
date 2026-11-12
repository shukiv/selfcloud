// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// groupView is the part of a group document that the plain output prints.
type groupView struct {
	ID         string  `json:"id"`
	ExternalID *string `json:"external_id"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

func groupPairs(body []byte) ([]output.KV, error) {
	var v groupView
	if err := decodeAs(body, &v, "a group"); err != nil {
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
		{Key: "created_at", Value: v.CreatedAt},
		{Key: "updated_at", Value: v.UpdatedAt},
	}, nil
}

func groupsRoute(acc string) string { return "/api/v1/accounts/" + acc + "/groups" }

// GroupCreate is `selfcloud group create --account ACC [--external-id ID] NAME`. A group that exists under the same name
// and external id is answered, not refused.
func GroupCreate(args []string, stdout, stderr io.Writer) int {
	c := begin("group create", true, stdout, stderr)
	var ext string
	c.set.StringVar(&ext, "external-id", "", "the caller's own `id` for the group, kept with it")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("group create takes one name"))
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
	resp, err := files.Send(cn, http.MethodPost, groupsRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, groupPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// GroupLs is `selfcloud group ls --account ACC [--all]`.
func GroupLs(args []string, stdout, stderr io.Writer) int {
	c := begin("group ls", true, stdout, stderr)
	var all bool
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("group ls", pos); err != nil {
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
	items, next, err := files.ListPages(cn, groupsRoute(acc), nil, all)
	if err != nil {
		return c.fail(err)
	}
	err = printList(c, items, next, []string{"ID", "NAME"}, func(raw []byte) ([]string, error) {
		var v groupView
		if err := decodeAs(raw, &v, "a group"); err != nil {
			return nil, err
		}
		return []string{v.ID, v.Name}, nil
	})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// oneGroup is the shared start of the commands that take one group id after the account.
func oneGroup(c *command, name string, args []string) (acc, group string, code int, ok bool) {
	pos, code, ok := c.parse(args)
	if !ok {
		return "", "", code, false
	}
	if len(pos) != 1 {
		return "", "", c.fail(exitcode.Usagef("%s takes one group id", name)), false
	}
	var err error
	if group, err = idArg("grp", "group", pos[0]); err != nil {
		return "", "", c.fail(err), false
	}
	if acc, err = c.accountID(); err != nil {
		return "", "", c.fail(err), false
	}
	return acc, group, 0, true
}

// GroupShow is `selfcloud group show --account ACC GROUP`.
func GroupShow(args []string, stdout, stderr io.Writer) int {
	c := begin("group show", true, stdout, stderr)
	acc, group, code, ok := oneGroup(c, "group show", args)
	if !ok {
		return code
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, groupsRoute(acc)+"/"+group, nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, groupPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// GroupRename is `selfcloud group rename --account ACC GROUP NAME`.
func GroupRename(args []string, stdout, stderr io.Writer) int {
	c := begin("group rename", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return c.fail(exitcode.Usagef("group rename takes a group id and a new name"))
	}
	group, err := idArg("grp", "group", pos[0])
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
	resp, err := files.Send(cn, http.MethodPatch, groupsRoute(acc)+"/"+group, nil, map[string]string{"name": name})
	if err != nil {
		return c.fail(err)
	}
	if err := printDocument(c, resp.Body, groupPairs); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// GroupRm is `selfcloud group rm --account ACC GROUP [--yes]`: removes a group, its members and every permission it
// holds. Asks first.
func GroupRm(args []string, stdout, stderr io.Writer) int {
	c := begin("group rm", true, stdout, stderr)
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	acc, group, code, ok := oneGroup(c, "group rm", args)
	if !ok {
		return code
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	route := groupsRoute(acc) + "/" + group
	if err := confirmByName(cn, route, yes, "remove the group, its members and the permissions it holds", stderr); err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, route, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, group); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// GroupMembers is `selfcloud group members --account ACC GROUP [--all]`.
func GroupMembers(args []string, stdout, stderr io.Writer) int {
	c := begin("group members", true, stdout, stderr)
	var all bool
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	acc, group, code, ok := oneGroup(c, "group members", args)
	if !ok {
		return code
	}
	cn, err := conn.Connect(c.g, stderr, true)
	if err != nil {
		return c.fail(err)
	}
	items, next, err := files.ListPages(cn, groupsRoute(acc)+"/"+group+"/members", nil, all)
	if err != nil {
		return c.fail(err)
	}
	err = printList(c, items, next, []string{"USER_ID", "ADDED"}, func(raw []byte) ([]string, error) {
		var v struct {
			UserID    string `json:"user_id"`
			CreatedAt string `json:"created_at"`
		}
		if err := decodeAs(raw, &v, "a group member"); err != nil {
			return nil, err
		}
		return []string{v.UserID, v.CreatedAt}, nil
	})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// member is GroupAddMember and GroupRemoveMember: they differ in the method only.
func member(name, method string, args []string, stdout, stderr io.Writer) int {
	c := begin(name, true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 2 {
		return c.fail(exitcode.Usagef("%s takes a group id and a user id", name))
	}
	group, err := idArg("grp", "group", pos[0])
	if err != nil {
		return c.fail(err)
	}
	user, err := idArg("usr", "user", pos[1])
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
	if _, err := files.Send(cn, method, groupsRoute(acc)+"/"+group+"/members/"+user, nil, nil); err != nil {
		return c.fail(err)
	}
	var werr error
	if c.g.JSON {
		werr = output.JSON(stdout, map[string]string{"group": group, "user": user, "status": "done"})
	} else {
		werr = output.KeyValues(stdout, []output.KV{
			{Key: "group", Value: group}, {Key: "user", Value: user}, {Key: "status", Value: "done"}})
	}
	if werr != nil {
		return c.fail(werr)
	}
	return exitcode.OK
}

// GroupAddMember is `selfcloud group add-member --account ACC GROUP USER`. Adding a member already in the group is done.
func GroupAddMember(args []string, stdout, stderr io.Writer) int {
	return member("group add-member", http.MethodPut, args, stdout, stderr)
}

// GroupRemoveMember is `selfcloud group remove-member --account ACC GROUP USER`.
func GroupRemoveMember(args []string, stdout, stderr io.Writer) int {
	return member("group remove-member", http.MethodDelete, args, stdout, stderr)
}

// UserGrants is `selfcloud user grants --account ACC USER`: the team spaces shared with a user, and at what level.
func UserGrants(args []string, stdout, stderr io.Writer) int {
	c := begin("user grants", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("user grants takes one user id"))
	}
	user, err := idArg("usr", "user", pos[0])
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
	// The API answers every grant in one page.
	items, next, err := files.ListPages(cn, "/api/v1/accounts/"+acc+"/users/"+user+"/grants", nil, true)
	if err != nil {
		return c.fail(err)
	}
	err = printList(c, items, next, []string{"FOLDER_ID", "LEVEL", "GRANTED"}, func(raw []byte) ([]string, error) {
		var v struct {
			FolderID  string `json:"folder_id"`
			Level     string `json:"level"`
			CreatedAt string `json:"created_at"`
		}
		if err := decodeAs(raw, &v, "a grant"); err != nil {
			return nil, err
		}
		return []string{v.FolderID, v.Level, v.CreatedAt}, nil
	})
	if err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
