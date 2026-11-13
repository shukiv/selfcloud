// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/shukiv/selfcloud/internal/cli/conn"
	"github.com/shukiv/selfcloud/internal/cli/exitcode"
	"github.com/shukiv/selfcloud/internal/cli/files"
	"github.com/shukiv/selfcloud/internal/cli/output"
	"github.com/shukiv/selfcloud/internal/config"
)

const storageUsersMax = 200

// storagePolicyView is the account's default limit for each user.
type storagePolicyView struct {
	UserQuotaBytes int64 `json:"user_quota_bytes"`
}

func storagePolicyRoute(acc string) string { return "/api/v1/accounts/" + acc + "/storage-policy" }
func storageRoute(acc string) string       { return "/api/v1/accounts/" + acc + "/storage" }
func storageLargestRoute(acc string) string {
	return storageRoute(acc) + "/largest"
}

// printStoragePolicy prints the policy from its view, so an --json answer never carries a field the command does not
// print.
func printStoragePolicy(c *command, body []byte) error {
	var v storagePolicyView
	if err := decodeAs(body, &v, "a storage policy"); err != nil {
		return err
	}
	if c.g.JSON {
		return output.JSON(c.stdout, v)
	}
	return output.KeyValues(c.stdout, []output.KV{{Key: "user_quota_bytes", Value: number(v.UserQuotaBytes)}})
}

// StoragePolicyShow is `selfcloud storage policy show [--account ACC]`.
func StoragePolicyShow(args []string, stdout, stderr io.Writer) int {
	c := begin("storage policy show", true, stdout, stderr)
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("storage policy show", pos); err != nil {
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
	resp, err := files.Send(cn, http.MethodGet, storagePolicyRoute(acc), nil, nil)
	if err != nil {
		return c.fail(err)
	}
	if err := printStoragePolicy(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// StoragePolicySet is `selfcloud storage policy set [--account ACC] --user-quota SIZE`. SIZE is bytes, or a size such
// as 10GiB; 0 is no limit.
func StoragePolicySet(args []string, stdout, stderr io.Writer) int {
	c := begin("storage policy set", true, stdout, stderr)
	var quotaText string
	c.set.StringVar(&quotaText, "user-quota", "", "each user's limit, a `size` such as 10GiB, or 0 for no limit")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("storage policy set", pos); err != nil {
		return c.fail(err)
	}
	if quotaText == "" {
		return c.fail(exitcode.Usagef("storage policy set needs --user-quota SIZE"))
	}
	var quota config.ByteSize
	if err := quota.UnmarshalText([]byte(quotaText)); err != nil {
		return c.fail(exitcode.Usagef("--user-quota: %s", err))
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	body := map[string]int64{"user_quota_bytes": int64(quota)}
	resp, err := files.Send(cn, http.MethodPatch, storagePolicyRoute(acc), nil, body)
	if err != nil {
		return c.fail(err)
	}
	if err := printStoragePolicy(c, resp.Body); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// accountUsageView is the account's own counters and limit.
type accountUsageView struct {
	UsedBytes      int64  `json:"used_bytes"`
	ReservedBytes  int64  `json:"reserved_bytes"`
	QuotaBytes     *int64 `json:"quota_bytes"`
	AvailableBytes *int64 `json:"available_bytes"`
	UserQuotaBytes int64  `json:"user_quota_bytes"`
}

// storageFolderView is a team space or personal folder's counters.
type storageFolderView struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Kind          string `json:"kind"`
	UsedBytes     int64  `json:"used_bytes"`
	UsedFiles     int64  `json:"used_files"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

// storageUserView is a member's totals.
type storageUserView struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	Email               string `json:"email"`
	Status              string `json:"status"`
	UsedBytes           int64  `json:"used_bytes"`
	ReservedBytes       int64  `json:"reserved_bytes"`
	QuotaBytes          *int64 `json:"quota_bytes"`
	EffectiveQuotaBytes *int64 `json:"effective_quota_bytes"`
}

// storageUsageView is what `storage usage` prints under --json.
type storageUsageView struct {
	Account accountUsageView    `json:"account"`
	Folders []storageFolderView `json:"folders"`
	Users   []storageUserView   `json:"users"`
}

// storageUsagePage is one page of the API's answer, with the cursor that follows it.
type storageUsagePage struct {
	Account         accountUsageView    `json:"account"`
	Folders         []storageFolderView `json:"folders"`
	Users           []storageUserView   `json:"users"`
	UsersNextCursor string              `json:"users_next_cursor"`
}

func number(v int64) string { return strconv.FormatInt(v, 10) }

func optionalNumber(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func usagePairs(v accountUsageView) []output.KV {
	return []output.KV{
		{Key: "used_bytes", Value: number(v.UsedBytes)},
		{Key: "reserved_bytes", Value: number(v.ReservedBytes)},
		{Key: "quota_bytes", Value: optionalNumber(v.QuotaBytes)},
		{Key: "available_bytes", Value: optionalNumber(v.AvailableBytes)},
		{Key: "user_quota_bytes", Value: number(v.UserQuotaBytes)},
	}
}

func usageFolderRow(v storageFolderView) []string {
	return []string{v.ID, v.Name, v.Kind, number(v.UsedBytes), number(v.UsedFiles), number(v.ReservedBytes)}
}

func usageUserRow(v storageUserView) []string {
	return []string{v.ID, v.Email, v.DisplayName, v.Status, number(v.UsedBytes), number(v.ReservedBytes),
		optionalNumber(v.QuotaBytes), optionalNumber(v.EffectiveQuotaBytes)}
}

// fetchUsagePage reads one page of an account's storage. The users cursor is users_cursor, not the cursor the other
// lists use.
func fetchUsagePage(c *command, cn *conn.Conn, acc, cursor string, limit int) (*storageUsagePage, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("users_cursor", cursor)
	}
	if limit > 0 {
		q.Set("users_limit", strconv.Itoa(limit))
	}
	resp, err := files.Send(cn, http.MethodGet, storageRoute(acc), q, nil)
	if err != nil {
		return nil, err
	}
	var page storageUsagePage
	page.Folders = []storageFolderView{}
	page.Users = []storageUserView{}
	if err := decodeAs(resp.Body, &page, "an account's storage"); err != nil {
		return nil, err
	}
	return &page, nil
}

// StorageUsage is `selfcloud storage usage [--account ACC] [--users-limit N] [--all]`.
func StorageUsage(args []string, stdout, stderr io.Writer) int {
	c := begin("storage usage", true, stdout, stderr)
	var usersLimitText string
	var all bool
	c.set.StringVar(&usersLimitText, "users-limit", "", "how many users to a page, from 1 to 200 (default 50)")
	c.set.BoolVar(&all, "all", false, "follow the users cursor and print every user")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("storage usage", pos); err != nil {
		return c.fail(err)
	}
	limit := 0
	if usersLimitText != "" {
		n, err := strconv.Atoi(usersLimitText)
		if err != nil || n < 1 || n > storageUsersMax {
			return c.fail(exitcode.Usagef("--users-limit needs a number from 1 to %d", storageUsersMax))
		}
		limit = n
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	cursor := ""
	first := true
	out := storageUsageView{Folders: []storageFolderView{}, Users: []storageUserView{}}
	for {
		page, err := fetchUsagePage(c, cn, acc, cursor, limit)
		if err != nil {
			return c.fail(err)
		}
		if first {
			out.Account, out.Folders, first = page.Account, page.Folders, false
		}
		out.Users = append(out.Users, page.Users...)
		if !all || page.UsersNextCursor == "" {
			cursor = page.UsersNextCursor
			break
		}
		if page.UsersNextCursor == cursor {
			return c.fail(errors.New("the server gave the same users cursor twice; the listing stopped"))
		}
		cursor = page.UsersNextCursor
	}
	if cursor != "" && !all {
		if _, err := io.WriteString(stderr, "selfcloud: more users exist; --all prints every page\n"); err != nil {
			return c.fail(err)
		}
	}
	if c.g.JSON {
		if err := output.JSON(stdout, out); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	if err := output.KeyValues(stdout, usagePairs(out.Account)); err != nil {
		return c.fail(err)
	}
	rows := make([][]string, 0, len(out.Folders))
	for _, f := range out.Folders {
		rows = append(rows, usageFolderRow(f))
	}
	if err := output.Table(stdout, []string{"FOLDER", "NAME", "KIND", "USED", "FILES", "RESERVED"}, rows); err != nil {
		return c.fail(err)
	}
	if len(out.Users) == 0 {
		return exitcode.OK
	}
	rows = rows[:0]
	for _, u := range out.Users {
		rows = append(rows, usageUserRow(u))
	}
	headers := []string{"PERSON", "EMAIL", "NAME", "STATUS", "USED", "RESERVED", "QUOTA", "EFFECTIVE QUOTA"}
	if err := output.Table(stdout, headers, rows); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}

// largeFileView is one file in a largest-files list. Both outputs use only these fields.
type largeFileView struct {
	ID            string `json:"id"`
	FolderID      string `json:"folder_id,omitempty"`
	FolderName    string `json:"folder_name,omitempty"`
	ParentName    string `json:"parent_name,omitempty"`
	Name          string `json:"name"`
	MimeType      string `json:"mime_type,omitempty"`
	Size          int64  `json:"size"`
	VersionsBytes int64  `json:"versions_bytes"`
	InTrash       bool   `json:"in_trash"`
	ModifiedAt    string `json:"modified_at"`
}

// StorageLargest is `selfcloud storage largest [--account ACC] [--folder FOLDER] [--all]`.
func StorageLargest(args []string, stdout, stderr io.Writer) int {
	c := begin("storage largest", true, stdout, stderr)
	var folder string
	var all bool
	c.set.StringVar(&folder, "folder", "", "only this folder's files, by `id` (fld_...)")
	c.set.BoolVar(&all, "all", false, "follow the cursor and print every page")
	pos, code, ok := c.parse(args)
	if !ok {
		return code
	}
	if err := noArgs("storage largest", pos); err != nil {
		return c.fail(err)
	}
	if folder != "" {
		id, err := idArg("fld", "folder", folder)
		if err != nil {
			return c.fail(err)
		}
		folder = id
	}
	acc, err := c.accountID()
	if err != nil {
		return c.fail(err)
	}
	cn, err := c.connect()
	if err != nil {
		return c.fail(err)
	}
	q := url.Values{}
	if folder != "" {
		q.Set("folder", folder)
	}
	items, next, err := files.ListPages(cn, storageLargestRoute(acc), q, all)
	if err != nil {
		return c.fail(err)
	}
	if next != "" {
		if _, err := io.WriteString(stderr, "selfcloud: more items exist; --all prints every page\n"); err != nil {
			return c.fail(err)
		}
	}
	views := make([]largeFileView, 0, len(items))
	for _, raw := range items {
		var v largeFileView
		if err := decodeAs(raw, &v, "a file"); err != nil {
			return c.fail(err)
		}
		views = append(views, v)
	}
	if c.g.JSON {
		if err := output.JSON(stdout, views); err != nil {
			return c.fail(err)
		}
		return exitcode.OK
	}
	rows := make([][]string, 0, len(views))
	for _, v := range views {
		rows = append(rows, []string{v.ID, v.Name, number(v.Size), number(v.VersionsBytes), v.FolderName, v.ParentName})
	}
	if err := output.Table(stdout, []string{"NODE", "NAME", "SIZE", "VERSIONS", "FOLDER", "PARENT"}, rows); err != nil {
		return c.fail(err)
	}
	return exitcode.OK
}
