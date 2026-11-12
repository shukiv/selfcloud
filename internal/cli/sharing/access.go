// SPDX-License-Identifier: AGPL-3.0-or-later

package sharing

import (
	"flag"
	"io"
	"net/url"

	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
)

// requestableRoles are the roles the API lets a request ask for, and a decision give.
var requestableRoles = []string{"viewer", "commenter", "editor"}

// AccessLs is `selfcloud access ls`: the access requests on a node, or with --pending the pending ones the caller may decide.
func AccessLs(args []string, stdout, stderr io.Writer) int {
	var pending, all bool
	r, code, ok := begin("access ls", args, stdout, stderr, func(set *flag.FlagSet) {
		set.BoolVar(&pending, "pending", false, "the pending requests, on every node the caller may share, instead of one node's")
		set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	})
	if !ok {
		return code
	}
	path := "/api/v1/access-requests"
	switch {
	case pending && len(r.pos) == 0:
	case !pending && len(r.pos) == 1:
		nodeID, err := idArg("nod", "a node", r.pos[0])
		if err != nil {
			return r.fail(err)
		}
		path = nodePath(nodeID, "/access-requests")
	default:
		return r.fail(exitcode.Usagef("access ls takes one node id, or --pending"))
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	items, next, err := files.ListPages(c, path, nil, all)
	if err != nil {
		return r.fail(err)
	}
	r.listNotice(next)
	return r.printList(items, []string{"ID", "NODE", "USER", "NAME", "ROLE", "STATUS", "CREATED"},
		[]string{"id", "node_id", "user_id", "user_name", "role", "status", "created_at"})
}

// AccessApprove is `selfcloud access approve`: gives the person the role asked for, or the one named with --role.
func AccessApprove(args []string, stdout, stderr io.Writer) int {
	var role string
	r, code, ok := begin("access approve", args, stdout, stderr, func(set *flag.FlagSet) {
		set.StringVar(&role, "role", "", "give this role instead of the one asked for: viewer, commenter or editor")
	})
	if !ok {
		return code
	}
	if role != "" && !validRole(role, requestableRoles) {
		return r.fail(exitcode.Usagef("--role is viewer, commenter or editor"))
	}
	var body any
	if role != "" {
		body = map[string]any{"role": role}
	}
	return r.decide("approve", body)
}

// AccessDeny is `selfcloud access deny`: refuses the requests.
func AccessDeny(args []string, stdout, stderr io.Writer) int {
	r, code, ok := begin("access deny", args, stdout, stderr, nil)
	if !ok {
		return code
	}
	return r.decide("deny", nil)
}

// decide approves or denies each request named, and reports each.
func (r *run) decide(verb string, body any) int {
	if len(r.pos) == 0 {
		return r.fail(exitcode.Usagef("access %s takes one or more request ids (arq_...)", verb))
	}
	for _, a := range r.pos {
		if _, err := idArg("arq", "an access request", a); err != nil {
			return r.fail(err)
		}
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	outs := make([]outcome, 0, len(r.pos))
	for _, a := range r.pos {
		if _, err := files.Send(c, "POST", "/api/v1/access-requests/"+a+"/"+verb, nil, body); err != nil {
			outs = append(outs, failedOutcome(a, err))
			continue
		}
		outs = append(outs, doneOutcome(a))
	}
	return r.report(outs)
}

// Shared is `selfcloud shared`: what other people shared with the caller.
func Shared(args []string, stdout, stderr io.Writer) int {
	var recent, all bool
	r, code, ok := begin("shared", args, stdout, stderr, func(set *flag.FlagSet) {
		set.BoolVar(&recent, "recent", false, "the most recently shared first")
		set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	})
	if !ok {
		return code
	}
	if len(r.pos) != 0 {
		return r.fail(exitcode.Usagef("shared takes no arguments"))
	}
	q := url.Values{}
	if recent {
		q.Set("order", "recent")
	}
	c, err := r.connect()
	if err != nil {
		return r.fail(err)
	}
	items, next, err := files.ListPages(c, "/api/v1/shared", q, all)
	if err != nil {
		return r.fail(err)
	}
	r.listNotice(next)
	return r.printList(items, []string{"ID", "KIND", "ROLE", "NAME", "SHARED", "PLACE"},
		[]string{"id", "kind", "role", "name", "shared_at", "place"})
}
