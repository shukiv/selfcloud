// SPDX-License-Identifier: AGPL-3.0-or-later

package webhook

import "net/url"

// Origin is the part of an endpoint URL that may be shown to anyone but the account key, and the only part of one that
// a log line, an audit entry or last_error may hold: its scheme and host, with the port if it has one. The path, the
// query, the fragment and any user name are dropped, because a token may sit in any of them. "" when the URL does not
// parse.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
