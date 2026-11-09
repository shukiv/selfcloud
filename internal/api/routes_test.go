// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"

	"go.yaml.in/yaml/v3"

	apispec "github.com/shukiv/selfcloud/api"
	"github.com/shukiv/selfcloud/internal/apierr"
)

type specDoc struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Enum []string `yaml:"enum"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) specDoc {
	t.Helper()
	var doc specDoc
	if err := yaml.Unmarshal(apispec.Spec, &doc); err != nil {
		t.Fatalf("api/openapi.yaml: %v", err)
	}
	return doc
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// parityRow is one row of docs/cli-parity.tsv (design section 5.1).
type parityRow struct {
	kind, key, command, slice, reason string
}

// parityColumns is the header line of docs/cli-parity.tsv.
const parityColumns = "kind\tkey\tcommand\tslice\treason"

// parityConfig pins the /metrics setting that the route guard depends on (design section 5.2): /metrics is always
// mounted, on the main listener, so a route test cannot flap with the setting.
func parityConfig(c *config.Config) {
	c.MetricsOnMain = true
	c.MetricsListen = ""
}

// moduleRoot returns the nearest directory above the test's package that holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test package")
		}
		dir = parent
	}
}

// readParity reads docs/cli-parity.tsv and fails the test on any problem parseParity finds.
func readParity(t *testing.T) []parityRow {
	t.Helper()
	f, err := os.Open(filepath.Join(moduleRoot(t), "docs", "cli-parity.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, problems, err := parseParity(f)
	if err != nil {
		t.Fatalf("docs/cli-parity.tsv: %v", err)
	}
	for _, p := range problems {
		t.Error(p)
	}
	return rows
}

// parseParity reads the table and checks its shape: the header, five fields per row, a kind, a key and a slice on every
// row, a reason on every row whose command is "-", no row twice, and the sort order (kind, then key). A malformed record
// is an error; the other checks are returned as problems.
func parseParity(in io.Reader) ([]parityRow, []string, error) {
	// A carriage return anywhere is an error: the table uses LF line ends, as the web reader requires. encoding/csv would
	// strip it without a word.
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, nil, err
	}
	if bytes.ContainsRune(data, '\r') {
		return nil, nil, fmt.Errorf("a carriage return; the table uses LF line ends")
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = '\t'
	r.LazyQuotes = false
	r.FieldsPerRecord = 5
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(recs) == 0 || strings.Join(recs[0], "\t") != parityColumns {
		return nil, nil, fmt.Errorf("the header line is not %q", parityColumns)
	}
	var problems []string
	rows := make([]parityRow, 0, len(recs)-1)
	for i, rec := range recs[1:] {
		row := parityRow{kind: rec[0], key: rec[1], command: rec[2], slice: rec[3], reason: rec[4]}
		line := i + 2
		if !slices.Contains([]string{"cmd", "fn", "route"}, row.kind) {
			problems = append(problems, fmt.Sprintf("docs/cli-parity.tsv line %d: kind %q is not cmd, fn or route", line, row.kind))
		}
		if row.key == "" || row.slice == "" {
			problems = append(problems, fmt.Sprintf("docs/cli-parity.tsv line %d: the key and the slice must not be empty", line))
		}
		if row.command == "-" && strings.TrimSpace(row.reason) == "" {
			problems = append(problems, fmt.Sprintf("docs/cli-parity.tsv line %d: %s %q has no command and no reason", line, row.kind, row.key))
		}
		if n := len(rows); n > 0 && !parityLess(rows[n-1], row) {
			problems = append(problems, fmt.Sprintf("docs/cli-parity.tsv line %d: %s %q is out of order or repeated", line, row.kind, row.key))
		}
		rows = append(rows, row)
	}
	return rows, problems, nil
}

// parityLess orders rows by kind, then by key.
func parityLess(a, b parityRow) bool {
	return a.kind < b.kind || (a.kind == b.kind && a.key < b.key)
}

// checkParityRoutes compares the routes the server mounts with the route rows of the table: each mounted route has a row,
// and each route row names a mounted route (design section 5.2).
func checkParityRoutes(t *testing.T, s *Server, rows []parityRow) {
	t.Helper()
	mounted := map[string]bool{}
	for _, rt := range s.routes() {
		mounted[rt.method+" "+rt.path] = true
	}
	inTable := map[string]bool{}
	for _, row := range rows {
		if row.kind != "route" {
			continue
		}
		inTable[row.key] = true
		if !mounted[row.key] {
			t.Errorf("docs/cli-parity.tsv has the route row %q, but the server does not mount that route", row.key)
		}
	}
	for key := range mounted {
		if !inTable[key] {
			t.Errorf("route %s has no row in docs/cli-parity.tsv: add one in the same commit", key)
		}
	}
}

// TestParityRowsNeedAReason feeds the table's shape check rows the check must refuse: a row with no command and an empty
// reason, a row with a blank reason, and a row out of order. A row with a command needs no reason.
func TestParityRowsNeedAReason(t *testing.T) {
	t.Parallel()
	bad := parityColumns + "\n" +
		"route\tGET /a\t-\tcli3\t\n" +
		"route\tGET /b\t-\tcli3\t   \n" +
		"route\tGET /d\t-\tcli3\tplanned, cli3\n" +
		"route\tGET /c\t-\tcli3\tplanned, cli3\n"
	_, problems, err := parseParity(strings.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 3 {
		t.Fatalf("problems = %v, want the two empty reasons and the order", problems)
	}
	for i, want := range []string{"GET /a", "GET /b", "out of order"} {
		if !strings.Contains(problems[i], want) {
			t.Errorf("problem %d = %q, want it to name %q", i, problems[i], want)
		}
	}
	good := parityColumns + "\nroute\tGET /a\tadd-a\tcli3\t\n"
	if _, problems, _ := parseParity(strings.NewReader(good)); len(problems) != 0 {
		t.Errorf("a row with a command needs no reason, but problems = %v", problems)
	}
}

// TestRoutesMatchSpec keeps api/openapi.yaml honest in both directions: every route is
// documented, and everything documented is served. It also keeps the route rows of the parity table in step with the
// routes (design section 5.2).
func TestRoutesMatchSpec(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)
	// /metrics is documented, and served only with metrics_on_main (sec1, decision 84), so the server here serves it.
	s := newTestServer(t, parityConfig)
	routed := map[string]bool{}
	for _, rt := range s.routes() {
		method := strings.ToLower(rt.method)
		routed[method+" "+rt.path] = true
		if _, ok := doc.Paths[rt.path][method]; !ok {
			t.Errorf("route %s %s is not in api/openapi.yaml", rt.method, rt.path)
		}
	}
	for path, ops := range doc.Paths {
		for method := range ops {
			if slices.Contains(httpMethods, method) && !routed[method+" "+path] {
				t.Errorf("api/openapi.yaml documents %s %s, but no route serves it",
					strings.ToUpper(method), path)
			}
		}
	}
	checkParityRoutes(t, s, readParity(t))
}

func TestErrorCodesMatchSpec(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)
	var codes []string
	for _, c := range apierr.Codes() {
		codes = append(codes, string(c))
	}
	slices.Sort(codes)
	spec := slices.Clone(doc.Components.Schemas["ErrorCode"].Enum)
	slices.Sort(spec)
	if !slices.Equal(codes, spec) {
		t.Errorf("ErrorCode enum in api/openapi.yaml:\n  %v\ndiffers from internal/apierr:\n  %v", spec, codes)
	}
}

// TestUsageSchemaMatchesResponse keeps the Usage schema and the usage reply in step: every field the
// reply carries is documented, and nothing documented is missing from the reply.
func TestUsageSchemaMatchesResponse(t *testing.T) {
	t.Parallel()
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(apispec.Spec, &doc); err != nil {
		t.Fatalf("api/openapi.yaml: %v", err)
	}
	documented := doc.Components.Schemas["Usage"].Properties
	var fields []string
	typ := reflect.TypeFor[usageJSON]()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		fields = append(fields, name)
		if _, ok := documented[name]; !ok {
			t.Errorf("usage reply field %s is not in the Usage schema", name)
		}
	}
	for name := range documented {
		if !slices.Contains(fields, name) {
			t.Errorf("Usage schema documents %s, which the usage reply does not carry", name)
		}
	}
}
