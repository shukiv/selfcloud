// SPDX-License-Identifier: AGPL-3.0-or-later

package manage

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

func accStorage() string       { return "/api/v1/accounts/" + testAcc + "/storage" }
func accStoragePolicy() string { return accStorage() + "-policy" }
func accLargest() string       { return accStorage() + "/largest" }

// storageDoc is an account's storage as the API answers it.
func storageDoc() map[string]any {
	return map[string]any{
		"account": map[string]any{
			"used_bytes": 100, "reserved_bytes": 5, "quota_bytes": nil, "available_bytes": nil,
			"user_quota_bytes": 0,
		},
		"folders": []any{
			map[string]any{"id": testFolder, "name": "Team", "kind": "team", "used_bytes": 90,
				"used_files": 3, "reserved_bytes": 4},
		},
		"users": []any{
			map[string]any{"id": testUser, "display_name": "Ann", "email": "ann@example.test", "status": "active",
				"used_bytes": 60, "reserved_bytes": 2, "quota_bytes": nil, "effective_quota_bytes": nil},
		},
	}
}

func storageUsageTable(users ...[]string) string {
	folders := table([]string{"FOLDER", "NAME", "KIND", "USED", "FILES", "RESERVED"},
		[]string{testFolder, "Team", "team", "90", "3", "4"})
	if len(users) == 0 {
		return folders
	}
	rows := append([][]string{{"PERSON", "EMAIL", "NAME", "STATUS", "USED", "RESERVED", "QUOTA", "EFFECTIVE QUOTA"}}, users...)
	return folders + table(rows...)
}

func storageAccountLines() string {
	return "used_bytes=100\nreserved_bytes=5\nquota_bytes=\navailable_bytes=\nuser_quota_bytes=0\n"
}

func TestStoragePolicyShow(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accStoragePolicy(), doc(http.StatusOK, map[string]any{"user_quota_bytes": 0}))
	res := f.runAcc(StoragePolicyShow)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accStoragePolicy())
	f.wantBearer(testAccountKey)
	if res.stdout != "user_quota_bytes=0\n" {
		t.Fatalf("stdout %q", res.stdout)
	}

	f.set("GET", accStoragePolicy(), doc(http.StatusOK, map[string]any{"user_quota_bytes": 10737418240}))
	res = f.runAcc(StoragePolicyShow, "--json")
	wantCode(t, res, 0)
	if res.stdout != "{\n  \"user_quota_bytes\": 10737418240\n}\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestStoragePolicyKeepsTheKnownFieldsOnly: an answer with extra secret-looking fields prints only the quota, plain or
// --json.
func TestStoragePolicyKeepsTheKnownFieldsOnly(t *testing.T) {
	for _, command := range []struct {
		name string
		run  func([]string, io.Writer, io.Writer) int
		args []string
	}{
		{"show", StoragePolicyShow, nil},
		{"set", StoragePolicySet, []string{"--user-quota", "1GiB"}},
	} {
		for _, jsonOut := range []bool{false, true} {
			isolate(t, testAccountKey)
			f := newFake(t)
			dirty := map[string]any{"user_quota_bytes": 1024, "token": "sck_secret_value",
				"secret_hash": "deadbeef", "password": "pw"}
			method, route := "GET", accStoragePolicy()
			if command.name == "set" {
				method = "PATCH"
			}
			f.on(method, route, doc(http.StatusOK, dirty))
			args := append([]string{}, command.args...)
			if jsonOut {
				args = append(args, "--json")
			}
			res := f.runAcc(command.run, args...)
			wantCode(t, res, 0)
			for _, secret := range []string{"sck_secret_value", "deadbeef", `"token"`, `"secret_hash"`, `"password"`} {
				if strings.Contains(res.stdout+res.stderr, secret) {
					t.Fatalf("%s --json=%v printed %q: stdout %q stderr %q", command.name, jsonOut, secret,
						res.stdout, res.stderr)
				}
			}
			if !strings.Contains(res.stdout, "1024") {
				t.Fatalf("%s --json=%v lost the quota: %q", command.name, jsonOut, res.stdout)
			}
		}
	}
}

func TestStoragePolicySet(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("PATCH", accStoragePolicy(), doc(http.StatusOK, map[string]any{"user_quota_bytes": 10737418240}))
	res := f.runAcc(StoragePolicySet, "--user-quota", "10GiB")
	wantCode(t, res, 0)
	f.wantCalls("PATCH " + accStoragePolicy())
	f.wantBearer(testAccountKey)
	if body := f.bodyOf(0); len(body) != 1 || body["user_quota_bytes"] != float64(10737418240) {
		t.Fatalf("body %v", body)
	}
	if res.stdout != "user_quota_bytes=10737418240\n" {
		t.Fatalf("stdout %q", res.stdout)
	}

	f.set("PATCH", accStoragePolicy(), doc(http.StatusOK, map[string]any{"user_quota_bytes": 0}))
	res = f.runAcc(StoragePolicySet, "--user-quota", "0")
	wantCode(t, res, 0)
	if got := f.bodyOf(1)["user_quota_bytes"]; got != float64(0) {
		t.Fatalf("body %v", got)
	}
}

func TestStoragePolicySetRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		nil,
		{"--user-quota", "-1"},
		{"--user-quota", "-1GiB"},
		{"--user-quota", "abc"},
		{"--user-quota", "1GiB", "extra"},
		{"--user-quota"},
	} {
		wantCode(t, f.runAcc(StoragePolicySet, args...), 2)
	}
	wantCode(t, f.run(StoragePolicySet, "--user-quota", "1GiB"), 2) // no account
	f.wantNoCalls()
}

func TestStorageUsage(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accStorage(), doc(http.StatusOK, storageDoc()))
	res := f.runAcc(StorageUsage)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accStorage())
	f.wantBearer(testAccountKey)
	want := storageAccountLines() + storageUsageTable([]string{testUser, "ann@example.test", "Ann", "active", "60", "2", "", ""})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestStorageUsageLimit(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accStorage(), doc(http.StatusOK, storageDoc()))
	res := f.runAcc(StorageUsage, "--users-limit", "2")
	wantCode(t, res, 0)
	f.wantCalls("GET " + accStorage() + "?users_limit=2")
	if !strings.Contains(res.stdout, "ann@example.test") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestStorageUsagePagesAndAll(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	second := id.Derive("usr", "second-user")
	firstPage := storageDoc()
	firstPage["users_next_cursor"] = "c2"
	secondPage := map[string]any{
		"account": map[string]any{"used_bytes": 100, "reserved_bytes": 5, "quota_bytes": nil,
			"available_bytes": nil, "user_quota_bytes": 0},
		"folders": []any{},
		"users": []any{map[string]any{"id": second, "display_name": "Bob", "email": "bob@example.test",
			"status": "active", "used_bytes": 1, "reserved_bytes": 0, "quota_bytes": 10,
			"effective_quota_bytes": 10}},
	}
	f.on("GET", accStorage(), doc(http.StatusOK, firstPage), doc(http.StatusOK, firstPage), doc(http.StatusOK, secondPage))
	res := f.runAcc(StorageUsage)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "bob@example.test") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}

	res = f.runAcc(StorageUsage, "--all", "--users-limit", "2")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accStorage(),
		"GET "+accStorage()+"?users_limit=2",
		"GET "+accStorage()+"?users_cursor=c2&users_limit=2")
	if !strings.Contains(res.stdout, "ann@example.test") || !strings.Contains(res.stdout, "bob@example.test") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestStorageUsageRepeatedCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	first := storageDoc()
	first["users_next_cursor"] = "c2"
	f.on("GET", accStorage(), doc(http.StatusOK, first), doc(http.StatusOK, first))
	res := f.runAcc(StorageUsage, "--all")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "same users cursor") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestStorageUsageJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accStorage(), doc(http.StatusOK, storageDoc()))
	res := f.runAcc(StorageUsage, "--json")
	wantCode(t, res, 0)
	for _, want := range []string{`"account"`, `"folders"`, `"users"`, `"user_quota_bytes": 0`,
		`"used_files": 3`, `"effective_quota_bytes": null`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
	if strings.Contains(res.stdout, "users_next_cursor") {
		t.Fatalf("stdout %q has a cursor", res.stdout)
	}
}

func TestStorageUsageRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	for _, args := range [][]string{
		{"--users-limit", "0"},
		{"--users-limit", "-1"},
		{"--users-limit", "201"},
		{"--users-limit", "abc"},
		{"extra"},
	} {
		wantCode(t, f.runAcc(StorageUsage, args...), 2)
	}
	wantCode(t, f.run(StorageUsage), 2) // no account
	f.wantNoCalls()
}

// largestDoc is one file as the API answers it in a largest-files list.
func largestDoc() map[string]any {
	return map[string]any{"id": id.Derive("nod", "largest"), "folder_id": testFolder, "folder_name": "Team",
		"parent_name": "Docs", "name": "big.bin", "mime_type": "application/octet-stream", "size": 900,
		"versions_bytes": 50, "in_trash": false, "modified_at": "2026-11-01T10:00:00Z"}
}

func TestStorageLargest(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := largestDoc()
	f.on("GET", accLargest(), page("", item))
	res := f.runAcc(StorageLargest)
	wantCode(t, res, 0)
	f.wantCalls("GET " + accLargest())
	f.wantBearer(testAccountKey)
	want := table([]string{"NODE", "NAME", "SIZE", "VERSIONS", "FOLDER", "PARENT"},
		[]string{item["id"].(string), "big.bin", "900", "50", "Team", "Docs"})
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

func TestStorageLargestFolderAndJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := largestDoc()
	f.on("GET", accLargest(), page("", item))
	res := f.runAcc(StorageLargest, "--folder", testFolder, "--json")
	wantCode(t, res, 0)
	f.wantCalls("GET " + accLargest() + "?folder=" + testFolder)
	for _, want := range []string{`"folder_id": "` + testFolder + `"`, `"parent_name": "Docs"`,
		`"versions_bytes": 50`, `"in_trash": false`, `"modified_at": "2026-11-01T10:00:00Z"`} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout %q lacks %q", res.stdout, want)
		}
	}
}

// TestStorageLargestJSONKeepsTheKnownFieldsOnly: an item with extra secret-looking fields prints only the known ones.
func TestStorageLargestJSONKeepsTheKnownFieldsOnly(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	item := largestDoc()
	item["token"] = "sck_secret_value"
	item["secret_hash"] = "deadbeef"
	f.on("GET", accLargest(), page("", item))
	res := f.runAcc(StorageLargest, "--json")
	wantCode(t, res, 0)
	for _, secret := range []string{"sck_secret_value", "deadbeef", `"token"`, `"secret_hash"`} {
		if strings.Contains(res.stdout+res.stderr, secret) {
			t.Fatalf("printed %q: stdout %q stderr %q", secret, res.stdout, res.stderr)
		}
	}
	if !strings.Contains(res.stdout, `"name": "big.bin"`) || !strings.Contains(res.stdout, `"versions_bytes": 50`) {
		t.Fatalf("stdout %q lost a known field", res.stdout)
	}
}

func TestStorageLargestPagesAndAll(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	first, second := largestDoc(), largestDoc()
	first["id"] = id.Derive("nod", "largest-first")
	second["id"] = id.Derive("nod", "largest-second")
	second["name"] = "second.bin"
	f.on("GET", accLargest(), page("c2", first), page("c2", first), page("", second))
	res := f.runAcc(StorageLargest, "--folder", testFolder)
	wantCode(t, res, 0)
	if !strings.Contains(res.stderr, "--all") || strings.Contains(res.stdout, "second.bin") {
		t.Fatalf("first page only, with a note: stdout %q stderr %q", res.stdout, res.stderr)
	}

	res = f.runAcc(StorageLargest, "--folder", testFolder, "--all")
	wantCode(t, res, 0)
	f.wantCalls("GET "+accLargest()+"?folder="+testFolder,
		"GET "+accLargest()+"?folder="+testFolder,
		"GET "+accLargest()+"?cursor=c2&folder="+testFolder)
	if !strings.Contains(res.stdout, "big.bin") || !strings.Contains(res.stdout, "second.bin") {
		t.Fatalf("stdout %q", res.stdout)
	}
}

func TestStorageLargestRepeatedCursor(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	f.on("GET", accLargest(), page("c2", largestDoc()), page("c2", largestDoc()))
	res := f.runAcc(StorageLargest, "--all")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "same cursor") {
		t.Fatalf("stderr %q", res.stderr)
	}
}

func TestStorageLargestRefusals(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	wantCode(t, f.runAcc(StorageLargest, "--folder", "fld_short"), 2)
	wantCode(t, f.runAcc(StorageLargest, "extra"), 2)
	wantCode(t, f.run(StorageLargest), 2) // no account
	f.wantNoCalls()

	f.on("GET", accLargest(), apiErr(http.StatusForbidden, "forbidden",
		"only the account owner or its key may list the largest files"))
	res := f.runAcc(StorageLargest)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "only the account owner or its key") {
		t.Fatalf("stderr %q", res.stderr)
	}
}
