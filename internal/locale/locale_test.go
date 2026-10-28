package locale

import (
	"slices"
	"testing"
)

func TestResolve(t *testing.T) {
	for in, want := range map[string]string{
		"en":      "en",
		"es":      "es",
		"FR":      "fr", // lower-cased
		"pt":      "pt", // exact
		"pt-BR":   "pt", // base language
		"pt-PT":   "pt", // base language
		"he-IL":   "he", // base language
		"ar-EG":   "ar", // base language
		"ru-RU":   "ru", // base language
		"en-GB":   "en", // base language
		"xx":      "en", // unknown
		"xx-YY":   "en", // unknown with a region
		"":        "en", // empty
		"  es  ":  "es", // trimmed
		"-":       "en", // no base language
		"de-AT":   "en", // a language we do not ship
		"pt-br":   "pt", // case does not matter
		"zh-Hant": "en", // a script we do not ship
	} {
		if got := Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSupportedIsTheSevenLanguages(t *testing.T) {
	want := []string{"en", "es", "fr", "pt", "ru", "ar", "he"}
	if !slices.Equal(Supported, want) {
		t.Errorf("Supported = %v", Supported)
	}
	for _, c := range Supported {
		if Resolve(c) != c {
			t.Errorf("%s does not resolve to itself", c)
		}
	}
}

func TestIsRTL(t *testing.T) {
	for in, want := range map[string]bool{
		"ar":    true,
		"he":    true,
		"he-IL": true,
		"ar-SA": true,
		"en":    false,
		"es":    false,
		"fr":    false,
		"pt-BR": false,
		"ru":    false,
		"xx":    false, // resolves to English
		"":      false,
	} {
		if got := IsRTL(in); got != want {
			t.Errorf("IsRTL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "en", // empty
		"   ":                     "en", // blank
		"%%%garbage":              "en", // unreadable
		"xx":                      "en", // unknown
		"de-DE,de;q=0.9":          "en", // nothing we ship
		"he-IL,en;q=0.8":          "he",
		"fr-CA,fr;q=0.9,en;q=0.8": "fr",
		"pt-BR,pt;q=0.9":          "pt",
		"ar;q=0.9, es;q=0.5":      "ar", // quality order
		"es;q=0.2, ru;q=0.9":      "ru",
		"en-US,en;q=0.9":          "en",
		"ru":                      "ru",
	} {
		if got := FromAcceptLanguage(in); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
