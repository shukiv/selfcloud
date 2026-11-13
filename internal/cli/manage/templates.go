// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// templateFolderAnswer is the answer of an account's templates folder route.
type templateFolderAnswer struct {
	NodeID *string `json:"node_id"`
}

// templateFolderPrint writes the folder: the view under --json, otherwise key=value lines.
func templateFolderPrint(c *command, body []byte) error {
	var v templateFolderAnswer
	if err := decodeAs(body, &v, "the templates folder"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	id := ""
	if v.NodeID != nil {
		id = *v.NodeID
	}
	return output.KeyValues(c.stdout, []output.KV{{Key: "node_id", Value: id}})
}

// TemplateFolderShow is `selfcloud template folder show`: the account folder that holds its templates.
func TemplateFolderShow(args []string, stdout, stderr io.Writer) int {
	c := begin("template folder show", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("template folder show", pos); err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodGet, "/api/v1/accounts/"+acc+"/templates", nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := templateFolderPrint(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// TemplateFolderSet is `selfcloud template folder set`: names the account folder that holds its templates, or clears it.
func TemplateFolderSet(args []string, stdout, stderr io.Writer) int {
	c := begin("template folder set", true, stdout, stderr)
	var clear bool
	c.set.BoolVar(&clear, "clear", false, "use no templates folder")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if clear == (len(pos) == 1) {
		return c.fail(exitcode.Usagef("give one folder, or --clear"))
	}
	if len(pos) > 1 {
		return c.fail(exitcode.Usagef("template folder set takes one folder id, or --clear"))
	}
	var body map[string]any
	if clear {
		body = map[string]any{"node_id": nil}
	} else {
		folder, err := idArg("nod", "node", pos[0])
		if err != nil {
			return c.fail(err)
		}
		body = map[string]any{"node_id": folder}
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPut, "/api/v1/accounts/"+acc+"/templates", nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := templateFolderPrint(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
