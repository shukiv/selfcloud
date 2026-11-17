package web

import (
	"html"
	"net/url"
	"strings"
)

// pageScripts is the built form of ui.scripts (decision 216): the tags the app page gains before </body>, and the
// script-src origins the policy gains. Both are built once for a list, so a request reads one whole answer.
type pageScripts struct {
	tags string
	src  string
}

// buildPageScripts builds the tags and the origins of a list of ui.scripts entries. Each entry becomes one tag, in list
// order, on its own line before </body>; an entry's URL origin joins script-src once, lower-cased with the default port
// dropped, and a same-origin path adds none.
func buildPageScripts(entries []string) *pageScripts {
	var tags strings.Builder
	var origins []string
	seen := make(map[string]bool)
	for _, e := range entries {
		tags.WriteString("\n<script src=\"")
		tags.WriteString(html.EscapeString(e))
		tags.WriteString("\" defer referrerpolicy=\"no-referrer\"></script>")
		if strings.HasPrefix(e, "/") {
			continue
		}
		u, err := url.Parse(e)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			continue
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		scheme := strings.ToLower(u.Scheme)
		port := u.Port()
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			port = ""
		}
		origin := scheme + "://" + host
		if port != "" {
			origin += ":" + port
		}
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	return &pageScripts{tags: tags.String(), src: strings.Join(origins, " ")}
}

// UseScripts swaps in the page scripts of a new ui.scripts list. A nil or empty list serves the page as it was built.
func (h *Handler) UseScripts(entries []string) {
	h.scripts.Store(buildPageScripts(entries))
}
