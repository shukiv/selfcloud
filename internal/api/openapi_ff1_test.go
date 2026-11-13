// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	apispec "github.com/shukiv/selfcloud/api"
)

// ff1Doc is the part of the spec the filter check reads: each operation's parameters (resolved to their names) and the
// Node schema's properties.
type ff1Doc struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Parameters map[string]map[string]any `yaml:"parameters"`
		Schemas    map[string]struct {
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// TestFF1RoutesDeclareTheirFilters: each file list the filters of decision 110 reaches names their parameters in
// api/openapi.yaml, and the Node schema has type. This is a narrow check of the ff1 routes only: a
// missing name fails it, and a route that is not listed here is not checked.
func TestFF1RoutesDeclareTheirFilters(t *testing.T) {
	var doc ff1Doc
	if err := yaml.Unmarshal(apispec.Spec, &doc); err != nil {
		t.Fatalf("api/openapi.yaml: %v", err)
	}
	filters := []string{"type", "modified_after", "modified_before"}
	routes := map[string][]string{
		"listChildren":           filters,
		"listChildrenByPath":     filters,
		"listStarred":            filters,
		"listRecent":             filters,
		"listTrash":              filters,
		"listShared":             filters,
		"getMyLargestFiles":      filters,
		"getAccountLargestFiles": filters,
		"searchNames":            append([]string{"kind"}, filters...),
	}
	// The operations, by ID, with their parameter names.
	names := map[string][]string{}
	for _, methods := range doc.Paths {
		for _, raw := range methods {
			op, ok := raw.(map[string]any)
			if !ok {
				continue // a path-level parameters list, not an operation
			}
			id, _ := op["operationId"].(string)
			if _, ok := routes[id]; !ok {
				continue
			}
			params, _ := op["parameters"].([]any)
			for _, rp := range params {
				p, ok := rp.(map[string]any)
				if !ok {
					continue
				}
				if ref, ok := p["$ref"].(string); ok {
					last := ref[strings.LastIndex(ref, "/")+1:]
					if def, ok := doc.Components.Parameters[last]; ok {
						if n, ok := def["name"].(string); ok {
							names[id] = append(names[id], n)
						}
					}
					continue
				}
				if n, ok := p["name"].(string); ok {
					names[id] = append(names[id], n)
				}
			}
		}
	}
	for id, want := range routes {
		got, found := names[id]
		if !found {
			t.Errorf("operation %s is not in api/openapi.yaml", id)
			continue
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("operation %s does not declare the %s parameter", id, w)
			}
		}
	}
	if _, ok := doc.Components.Schemas["Node"].Properties["type"]; !ok {
		t.Error("the Node schema has no type property")
	}
}
