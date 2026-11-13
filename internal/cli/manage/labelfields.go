// SPDX-License-Identifier: AGPL-3.0-or-later

// The label field commands (slice cli9b4): label field add|update|rm. They define the fields an
// account labels its files with, as the web interface's settings page does. A field is named by its lbf_ id; a choice by
// its lbc_ id. The account owner and its account key define labels; an installation admin and a device token do not.

package manage

import (
	"flag"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// labelChoiceView is the part of a choice document that the plain output prints.
type labelChoiceView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// labelFieldView is the part of a field document that the command prints, plain or under --json: a field the API adds
// is not shown until it is added here.
type labelFieldView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Restricted bool              `json:"restricted"`
	Choices    []labelChoiceView `json:"choices"`
}

func (v labelFieldView) pairs() []output.KV {
	kv := []output.KV{
		{Key: "id", Value: v.ID},
		{Key: "name", Value: v.Name},
		{Key: "type", Value: v.Type},
		{Key: "restricted", Value: strconv.FormatBool(v.Restricted)},
	}
	for _, c := range v.Choices {
		value := c.ID + " " + c.Name
		if c.Archived {
			value += " (archived)"
		}
		kv = append(kv, output.KV{Key: "choice", Value: value})
	}
	return kv
}

// printLabelField prints the field a change answered with, from the allow-list view: a field the API adds is not shown
// until labelFieldView has it.
func printLabelField(c *command, body []byte) error {
	var v labelFieldView
	if err := decodeAs(body, &v, "a label field"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, v.pairs())
}

func labelFieldsRoute(acc string) string { return "/api/v1/accounts/" + acc + "/label-fields" }

// labelTypes is the field types the API accepts. A type is checked here so a bad one costs no request.
var labelTypes = []string{"text", "number", "date", "user", "choice"}

func labelTypeArg(typ string) (string, error) {
	for _, t := range labelTypes {
		if typ == t {
			return typ, nil
		}
	}
	return "", exitcode.Usagef("the type must be one of %s", strings.Join(labelTypes, ", "))
}

// labelRestrictedArg reads a true or false that must be given with the flag, as --restricted false.
func labelRestrictedArg(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, exitcode.Usagef("--restricted takes true or false")
}

// labelChoiceFlag collects a flag that may be given more than once.
type labelChoiceFlag []string

func (f *labelChoiceFlag) String() string { return strings.Join(*f, ",") }

func (f *labelChoiceFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

var _ flag.Value = (*labelChoiceFlag)(nil)

// labelChoiceBody is one choice in a create or update call. Archived is set only when a choice is archived or restored;
// a choice left without it keeps its flag.
type labelChoiceBody struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Archived *bool  `json:"archived,omitempty"`
}

// LabelFieldAdd is `selfcloud label field add [--account ACC] --type TYPE [--restricted] [--choice NAME]... NAME`.
func LabelFieldAdd(args []string, stdout, stderr io.Writer) int {
	c := begin("label field add", true, stdout, stderr)
	var typ string
	var restricted bool
	var choiceNames labelChoiceFlag
	c.set.StringVar(&typ, "type", "", "the field's `type`: text, number, date, user or choice")
	c.set.BoolVar(&restricted, "restricted", false, "only managers see this field's values")
	c.set.Var(&choiceNames, "choice", "a choice of a choice field, by `name`; may be given more than once")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("label field add takes one name"))
	}
	name, err := nameArg(pos[0])
	if err != nil {
		return c.fail(err)
	}
	if typ == "" {
		return c.fail(exitcode.Usagef("label field add needs --type: one of %s", strings.Join(labelTypes, ", ")))
	}
	if typ, err = labelTypeArg(typ); err != nil {
		return c.fail(err)
	}
	choices := make([]labelChoiceBody, 0, len(choiceNames))
	for _, choiceName := range choiceNames {
		cleaned, err := nameArg(choiceName)
		if err != nil {
			return c.fail(err)
		}
		choices = append(choices, labelChoiceBody{Name: cleaned})
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	body := map[string]any{"name": name, "type": typ, "restricted": restricted, "choices": choices}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	resp, err := files.Send(cn, http.MethodPost, labelFieldsRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printLabelField(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// labelChoiceNames reads the field's choices and returns their names by id. The list route has no account in its path:
// the account is the credential's own, which is the one that may change the field.
func labelChoiceNames(cn *conn.Conn, fieldID string) (map[string]string, error) {
	resp, err := files.Send(cn, http.MethodGet, "/api/v1/label-fields", nil, nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []labelFieldView `json:"items"`
	}
	if err := decodeAs(resp.Body, &list, "a list of label fields"); err != nil {
		return nil, err
	}
	for _, f := range list.Items {
		if f.ID != fieldID {
			continue
		}
		names := make(map[string]string, len(f.Choices))
		for _, choice := range f.Choices {
			names[choice.ID] = choice.Name
		}
		return names, nil
	}
	return nil, exitcode.NotFoundf("no such label field")
}

// LabelFieldUpdate is `selfcloud label field update [--account ACC] FIELD [--name NAME] [--type TYPE]
// [--restricted true|false] [--add-choice NAME]... [--rename-choice ID=NAME]... [--archive-choice ID]...
// [--restore-choice ID]...`: it changes only what is given.
func LabelFieldUpdate(args []string, stdout, stderr io.Writer) int {
	c := begin("label field update", true, stdout, stderr)
	var name, typ, restricted string
	var adds, renames, archives, restores labelChoiceFlag
	c.set.StringVar(&name, "name", "", "the field's new `name`")
	c.set.StringVar(&typ, "type", "", "the field's new `type`: text, number, date, user or choice")
	c.set.StringVar(&restricted, "restricted", "", "only managers see this field's values: true or false")
	c.set.Var(&adds, "add-choice", "a new choice, by `name`; may be given more than once")
	c.set.Var(&renames, "rename-choice", "a choice named by its `id=name`; may be given more than once")
	c.set.Var(&archives, "archive-choice", "a choice named by its `id`, kept for the values that use it")
	c.set.Var(&restores, "restore-choice", "a choice named by its `id`, back in the list")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	seen := map[string]bool{}
	c.set.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("label field update takes one label field id"))
	}
	field, err := idArg("lbf", "label field", pos[0])
	if err != nil {
		return c.fail(err)
	}
	if !seen["name"] && !seen["type"] && !seen["restricted"] && len(adds)+len(renames)+len(archives)+len(restores) == 0 {
		return c.fail(exitcode.Usagef("label field update needs a name, a type, a restriction or a choice to change"))
	}
	body := map[string]any{}
	if seen["name"] {
		cleaned, err := nameArg(name)
		if err != nil {
			return c.fail(err)
		}
		body["name"] = cleaned
	}
	if seen["type"] {
		cleaned, err := labelTypeArg(typ)
		if err != nil {
			return c.fail(err)
		}
		body["type"] = cleaned
	}
	if seen["restricted"] {
		value, err := labelRestrictedArg(restricted)
		if err != nil {
			return c.fail(err)
		}
		body["restricted"] = value
	}
	type namedChoice struct {
		id   string
		name string
	}
	renamed := make([]namedChoice, 0, len(renames))
	for _, pair := range renames {
		idPart, namePart, ok := strings.Cut(pair, "=")
		if !ok {
			return c.fail(exitcode.Usagef("--rename-choice takes id=name"))
		}
		choiceID, err := idArg("lbc", "choice", idPart)
		if err != nil {
			return c.fail(err)
		}
		cleaned, err := nameArg(namePart)
		if err != nil {
			return c.fail(err)
		}
		renamed = append(renamed, namedChoice{id: choiceID, name: cleaned})
	}
	archivedIDs := make([]string, 0, len(archives)+len(restores))
	for _, raw := range archives {
		choiceID, err := idArg("lbc", "choice", raw)
		if err != nil {
			return c.fail(err)
		}
		archivedIDs = append(archivedIDs, choiceID)
	}
	restoredIDs := make([]string, 0, len(restores))
	for _, raw := range restores {
		choiceID, err := idArg("lbc", "choice", raw)
		if err != nil {
			return c.fail(err)
		}
		restoredIDs = append(restoredIDs, choiceID)
	}
	added := make([]string, 0, len(adds))
	for _, raw := range adds {
		cleaned, err := nameArg(raw)
		if err != nil {
			return c.fail(err)
		}
		added = append(added, cleaned)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	var names map[string]string
	if len(archivedIDs)+len(restoredIDs) > 0 {
		if names, err = labelChoiceNames(cn, field); err != nil {
			return c.fail(err)
		}
	}
	choices := make([]labelChoiceBody, 0, len(added)+len(renamed)+len(archivedIDs)+len(restoredIDs))
	for _, choiceName := range added {
		choices = append(choices, labelChoiceBody{Name: choiceName})
	}
	for _, r := range renamed {
		choices = append(choices, labelChoiceBody{ID: r.id, Name: r.name})
	}
	archiveFlag := func(on bool) *bool { return &on }
	for _, choiceID := range archivedIDs {
		choiceName, ok := names[choiceID]
		if !ok {
			return c.fail(exitcode.Usagef("the label field has no choice %s", choiceID))
		}
		choices = append(choices, labelChoiceBody{ID: choiceID, Name: choiceName, Archived: archiveFlag(true)})
	}
	for _, choiceID := range restoredIDs {
		choiceName, ok := names[choiceID]
		if !ok {
			return c.fail(exitcode.Usagef("the label field has no choice %s", choiceID))
		}
		choices = append(choices, labelChoiceBody{ID: choiceID, Name: choiceName, Archived: archiveFlag(false)})
	}
	if len(choices) > 0 {
		body["choices"] = choices
	}
	resp, err := files.Send(cn, http.MethodPatch, labelFieldsRoute(acc)+"/"+field, nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printLabelField(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// LabelFieldRm is `selfcloud label field rm [--account ACC] FIELD [--yes]`: removes a field and every value it holds on
// files. It asks first, by the field's id, and refuses without a terminal and without --yes before a request is sent.
func LabelFieldRm(args []string, stdout, stderr io.Writer) int {
	c := begin("label field rm", true, stdout, stderr)
	var yes bool
	c.set.BoolVar(&yes, "yes", false, "do not ask first (needed when standard input is not a terminal)")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.fail(exitcode.Usagef("label field rm takes one label field id"))
	}
	field, err := idArg("lbf", "label field", pos[0])
	if err != nil {
		return c.fail(err)
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	if err := files.Confirm(yes, field, "remove the label field and every value it holds on files", stderr); err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	if _, err := files.Send(cn, http.MethodDelete, labelFieldsRoute(acc)+"/"+field, nil, nil); err != nil {
		return c.fail(err)
	}
	if err := printDone(c, field); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
