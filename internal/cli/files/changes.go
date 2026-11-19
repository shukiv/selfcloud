// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/cli/client"
	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/output"
)

// followContext is the context --follow runs under, cancelled by an interrupt. A test replaces it to stop the follow.
var followContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// followWait is the long poll --follow asks for: under the client's 30 s header timeout, so a held wait answers before
// the client gives up on it.
const followWait = 25

// followSleep waits before the next follow poll. Tests make it return at once.
var followSleep = derivedSleep

// followRetryAfter reads a Retry-After header as a positive whole-second wait capped at thirty seconds, or five seconds
// when it cannot be read: the backoff an empty page asks for before the follow polls again.
func followRetryAfter(h string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n > 0 {
		return min(time.Duration(n)*time.Second, 30*time.Second)
	}
	return 5 * time.Second
}

// changesFeed is the feed's answer: the folded changes, the cursor to read after next, and whether rows remain.
type changesFeed struct {
	Changes []changesEntry `json:"changes"`
	Cursor  string         `json:"cursor"`
	HasMore bool           `json:"has_more"`
}

// changesEntry is one change. Node is the node as a stat answers it while it is live, and absent once it is removed.
type changesEntry struct {
	NodeID  string          `json:"node_id"`
	Removed bool            `json:"removed"`
	Relist  bool            `json:"relist"`
	Node    json.RawMessage `json:"node"`
}

// Changes is `selfcloud changes`: what changed in a space (or the whole account) since a cursor. Without --cursor it
// starts at the feed's head; --follow keeps reading, waiting for each next change, until interrupted.
func Changes(args []string, stdout, stderr io.Writer) int {
	set := conn.NewFlagSet("changes")
	var g conn.Global
	g.Register(set)
	var space, cursor string
	var limit int
	var follow bool
	set.StringVar(&space, "space", "", "the `id` (fld_...) of the space to watch; without it, the account's whole feed")
	set.StringVar(&cursor, "cursor", "", "read after this `token`; without it, start at the feed's head")
	set.IntVar(&limit, "limit", 200, "the most changes one page carries (1..1000)")
	set.BoolVar(&follow, "follow", false, "keep reading, waiting for each next change, until interrupted")
	pos, code, ok := parse(set, &g, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return fail(stderr, g, exitcode.Usagef("changes takes no arguments"))
	}
	if space != "" {
		if _, err := folderArg(space); err != nil {
			return fail(stderr, g, err)
		}
	}
	if limit < 1 || limit > 1000 {
		return fail(stderr, g, exitcode.Usagef("--limit is between 1 and 1000"))
	}
	c, err := connect(g, stderr)
	if err != nil {
		return fail(stderr, g, err)
	}

	readPage := func(ctx context.Context, cur string, wait int) (*client.Response, changesFeed, error) {
		q := url.Values{"cursor": {cur}, "limit": {strconv.Itoa(limit)}}
		if space != "" {
			q.Set("space", space)
		}
		if wait > 0 {
			q.Set("wait", strconv.Itoa(wait))
		}
		resp, err := c.Client.Do(ctx, client.Request{Method: http.MethodGet, Path: "/api/v1/changes", Query: q, Accept: "application/json"})
		if err != nil {
			return nil, changesFeed{}, err
		}
		var feed changesFeed
		if err := json.Unmarshal(resp.Body, &feed); err != nil {
			return nil, changesFeed{}, errors.New("the server's answer is not a changes feed")
		}
		return resp, feed, nil
	}

	if cursor == "" {
		q := url.Values{}
		if space != "" {
			q.Set("space", space)
		}
		resp, err := send(c, request{method: http.MethodGet, path: "/api/v1/changes/start", query: q})
		if err != nil {
			return fail(stderr, g, err)
		}
		var start struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(resp.Body, &start); err != nil {
			return fail(stderr, g, errors.New("the server's answer is not a cursor"))
		}
		cursor = start.Cursor
	}

	if follow {
		ctx, cancel := followContext()
		defer cancel()
		for ctx.Err() == nil {
			start := time.Now()
			resp, feed, err := readPage(ctx, cursor, followWait)
			if err != nil {
				if ctx.Err() != nil {
					return exitcode.OK
				}
				if code, handled := changesGone(stderr, err); handled {
					return code
				}
				return fail(stderr, g, err)
			}
			if err := printChanges(stdout, g, resp.Body, feed); err != nil {
				return fail(stderr, g, err)
			}
			cursor = feed.Cursor
			// An empty page that came back well before the wait (a refused wait, or a server that does not hold)
			// backs off before polling again, so the follow does not spin; a page that held the wait needs no sleep.
			if len(feed.Changes) == 0 && !feed.HasMore && time.Since(start) < time.Duration(followWait)*time.Second/2 {
				if err := followSleep(ctx, followRetryAfter(resp.Header.Get("Retry-After"))); err != nil {
					return exitcode.OK
				}
			}
		}
		return exitcode.OK
	}

	resp, feed, err := readPage(context.Background(), cursor, 0)
	if err != nil {
		if code, handled := changesGone(stderr, err); handled {
			return code
		}
		return fail(stderr, g, err)
	}
	if err := printChanges(stdout, g, resp.Body, feed); err != nil {
		return fail(stderr, g, err)
	}
	return exitcode.OK
}

// changesGone reports a 410 gone answer plainly, in plain and JSON output alike, and returns exit 1: the cursor fell
// below the prune mark, so the client must start again. It returns false for any other error.
func changesGone(stderr io.Writer, err error) (int, bool) {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusGone {
		_, _ = fmt.Fprintf(stderr, "selfcloud: %s\n", ae.Message)
		return exitcode.Failed, true
	}
	return 0, false
}

// printChanges writes one page: the API's own document under --json, otherwise a line per change and the cursor last.
func printChanges(stdout io.Writer, g conn.Global, raw []byte, feed changesFeed) error {
	if g.JSON {
		return output.Indented(stdout, raw)
	}
	rows := make([][]string, 0, len(feed.Changes))
	for _, e := range feed.Changes {
		state, name := "changed", ""
		if e.Removed {
			state = "removed"
		} else {
			if e.Relist {
				state = "relist"
			}
			if len(e.Node) > 0 {
				var v nodeView
				if err := json.Unmarshal(e.Node, &v); err == nil {
					name = v.Name
				}
			}
		}
		rows = append(rows, []string{e.NodeID, state, name})
	}
	if err := output.Table(stdout, []string{"ID", "STATE", "NAME"}, rows); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "cursor %s\n", feed.Cursor)
	return err
}
