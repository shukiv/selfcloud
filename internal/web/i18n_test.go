// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// catalogueSet is one source tree's catalogues: the Svelte build's, which keeps "x.one" plural keys,
// and the React build's, which keeps "x_one" (i18next's form). Both are checked until the flip, when
// the Svelte one goes.
type catalogueSet struct {
	name string
	dir  string
	// sep joins a plural key to its category: "." in the Svelte catalogues, "_" in the React ones.
	sep string
}

var catalogueSets = []catalogueSet{
	{name: "svelte", dir: "../../web/src/i18n", sep: "."},
	{name: "react", dir: "../../webui/src/i18n", sep: "_"},
}

var languages = []string{"en", "es", "fr", "pt", "ru", "ar", "he"}

var (
	placeholderPattern = regexp.MustCompile(`\{[A-Za-z]+\}`)
	pluralSuffixes     = []string{"zero", "one", "two", "few", "many", "other"}
)

func loadCatalogue(t *testing.T, dir, lang string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, lang+".json"))
	if err != nil {
		t.Fatalf("read %s catalogue: %v", lang, err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s catalogue is not a flat JSON object of strings: %v", lang, err)
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestCataloguesHaveTheSameKeys fails when a key is missing from any of the seven catalogues, or
// present in one and absent from another. A plural key has one entry per plural category, so
// every language also has the same categories for each plural key.
func TestCataloguesHaveTheSameKeys(t *testing.T) {
	for _, set := range catalogueSets {
		t.Run(set.name, func(t *testing.T) {
			en := loadCatalogue(t, set.dir, "en")
			want := sortedKeys(en)
			if len(want) == 0 {
				t.Fatal("the English catalogue is empty")
			}
			for _, lang := range languages[1:] {
				got := loadCatalogue(t, set.dir, lang)
				if missing := diff(want, got); len(missing) > 0 {
					t.Errorf("%s.json lacks keys: %s", lang, strings.Join(missing, ", "))
				}
				if extra := diff(sortedKeys(got), en); len(extra) > 0 {
					t.Errorf("%s.json has keys that English lacks: %s", lang, strings.Join(extra, ", "))
				}
			}
		})
	}
}

// diff returns the keys of want that are not in m.
func diff(want []string, m map[string]string) []string {
	var out []string
	for _, k := range want {
		if _, ok := m[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// TestCataloguesHaveNoEmptyValues fails on an empty string, which would show as a blank label.
func TestCataloguesHaveNoEmptyValues(t *testing.T) {
	for _, set := range catalogueSets {
		t.Run(set.name, func(t *testing.T) {
			for _, lang := range languages {
				for k, v := range loadCatalogue(t, set.dir, lang) {
					if strings.TrimSpace(v) == "" {
						t.Errorf("%s.json: %q is empty", lang, k)
					}
				}
			}
		})
	}
}

// TestPluralFormsAreComplete checks that every plural key carries the full set of categories
// (zero to other) in every language, so a count always finds its form.
func TestPluralFormsAreComplete(t *testing.T) {
	for _, set := range catalogueSets {
		t.Run(set.name, func(t *testing.T) {
			en := loadCatalogue(t, set.dir, "en")
			for k := range en {
				base, ok := strings.CutSuffix(k, set.sep+"other")
				if !ok {
					continue
				}
				for _, lang := range languages {
					m := loadCatalogue(t, set.dir, lang)
					for _, cat := range pluralSuffixes {
						if _, ok := m[base+set.sep+cat]; !ok {
							t.Errorf("%s.json lacks %s%s%s", lang, base, set.sep, cat)
						}
					}
				}
			}
		})
	}
}

// TestPlaceholdersMatch fails when a sentence in one language uses different {placeholders} from
// English. For a plural key the check is on its "other" form, which every language has.
func TestPlaceholdersMatch(t *testing.T) {
	for _, set := range catalogueSets {
		t.Run(set.name, func(t *testing.T) {
			en := loadCatalogue(t, set.dir, "en")
			for _, lang := range languages[1:] {
				m := loadCatalogue(t, set.dir, lang)
				for k, enV := range en {
					if isPluralForm(k, set.sep) {
						continue
					}
					v, ok := m[k]
					if !ok {
						continue // reported by TestCataloguesHaveTheSameKeys
					}
					if !slices.Equal(placeholders(enV), placeholders(v)) {
						t.Errorf("%s.json: %q uses placeholders %v, English uses %v",
							lang, k, placeholders(v), placeholders(enV))
					}
				}
			}
		})
	}
}

// isPluralForm reports whether key ends in a plural category other than "other", which is the form
// the placeholder check reads for a plural key.
func isPluralForm(key, sep string) bool {
	for _, cat := range []string{"zero", "one", "two", "few", "many"} {
		if strings.HasSuffix(key, sep+cat) {
			return true
		}
	}
	return false
}

// placeholders returns the sorted, de-duplicated {names} in s.
func placeholders(s string) []string {
	seen := map[string]bool{}
	for _, p := range placeholderPattern.FindAllString(s, -1) {
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
