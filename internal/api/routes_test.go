package api

import (
	"reflect"
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

// TestUsageSchemaMatchesResponse keeps the Usage schema and the usage reply in step: every field the
// reply carries is documented, and nothing documented is missing from the reply.
func TestUsageSchemaMatchesResponse(t *testing.T) {
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
