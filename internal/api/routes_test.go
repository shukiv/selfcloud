package api

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	apispec "selfcloud/api"
	"selfcloud/internal/apierr"
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

// TestRoutesMatchSpec keeps api/openapi.yaml honest in both directions: every route is
// documented, and everything documented is served.
func TestRoutesMatchSpec(t *testing.T) {
	doc := loadSpec(t)
	s := newTestServer(t)
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
}

func TestErrorCodesMatchSpec(t *testing.T) {
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
