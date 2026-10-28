// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import "github.com/shukiv/selfcloud/internal/locale"

// The roles a folder can be shared with. A mail names the role in words, so each one has a label in
// every language. The keys are the role names the API uses.
const (
	RoleViewer      = "viewer"
	RoleCommenter   = "commenter"
	RoleContributor = "contributor"
	RoleEditor      = "editor"
	RoleManager     = "manager"
)

// roleLabels holds the label of each role, by language. The labels are lower case and sit in the
// middle of a sentence, so they are base nouns, not titles. Hebrew uses the masculine form.
var roleLabels = map[string]map[string]string{
	"en": {
		RoleViewer: "viewer", RoleCommenter: "commenter", RoleContributor: "contributor",
		RoleEditor: "editor", RoleManager: "manager",
	},
	"es": {
		RoleViewer: "lector", RoleCommenter: "comentarista", RoleContributor: "colaborador",
		RoleEditor: "editor", RoleManager: "gestor",
	},
	"fr": {
		RoleViewer: "lecteur", RoleCommenter: "commentateur", RoleContributor: "contributeur",
		RoleEditor: "éditeur", RoleManager: "gestionnaire",
	},
	"pt": {
		RoleViewer: "leitor", RoleCommenter: "comentador", RoleContributor: "colaborador",
		RoleEditor: "editor", RoleManager: "gestor",
	},
	"ru": {
		RoleViewer: "читатель", RoleCommenter: "комментатор", RoleContributor: "участник",
		RoleEditor: "редактор", RoleManager: "менеджер",
	},
	"ar": {
		RoleViewer: "مشاهد", RoleCommenter: "معلِّق", RoleContributor: "مساهم",
		RoleEditor: "محرِّر", RoleManager: "مدير",
	},
	"he": {
		RoleViewer: "צופה", RoleCommenter: "מעיר", RoleContributor: "תורם",
		RoleEditor: "עורך", RoleManager: "מנהל",
	},
}

// RoleLabel returns the word for role in the language of loc, resolved as locale.Resolve does, so
// "pt-BR" gets the Portuguese word. A language without the word falls back to English. An unknown
// role gives "", so a caller can tell a role it does not know from a label it has.
func RoleLabel(loc, role string) string {
	if label, ok := roleLabels[locale.Resolve(loc)][role]; ok {
		return label
	}
	return roleLabels[locale.Default][role]
}

// genericItems is the word for an item the mail does not name, in each supported language. A denial
// mail uses it, so it never reveals the name of what was asked for.
var genericItems = map[string]string{
	"en": "an item", "es": "un elemento", "fr": "un élément", "pt": "um item",
	"ru": "объект", "ar": "عنصر", "he": "פריט",
}

// GenericItemLabel returns the word for an unnamed item in the locale, falling back to English.
func GenericItemLabel(loc string) string {
	if w, ok := genericItems[locale.Resolve(loc)]; ok {
		return w
	}
	return genericItems["en"]
}
