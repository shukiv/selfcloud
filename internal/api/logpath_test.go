package api

import "testing"

// TestLogPathRedactsLinks checks that a signed link's token never reaches the log, however the
// path is spelled. The mux answers an unclean path with a redirect that matches no route, so the
// raw path would otherwise be logged.
func TestLogPathRedactsLinks(t *testing.T) {
	info := &reqInfo{route: "unmatched"}
	for _, p := range []string{
		"/api/v1/links/TOKEN",
		"//api/v1//links/TOKEN",
		"/api/v1/./links/TOKEN",
		"/api/v1/x/../links/TOKEN",
		"/API/V1/LINKS/TOKEN",
		"/links/TOKEN",
	} {
		if got := logPath(info, p); got != "unmatched" {
			t.Errorf("logPath(%q) = %q, want the route label", p, got)
		}
	}
	for _, p := range []string{"/api/v1/nodes/nod_1", "/api/v1/linksx/a", "/healthz"} {
		if got := logPath(info, p); got != p {
			t.Errorf("logPath(%q) = %q, want it unchanged", p, got)
		}
	}
	secret := &reqInfo{route: "POST /api/v1/auth/verify", secretPath: true}
	if got := logPath(secret, "/api/v1/auth/verify"); got != secret.route {
		t.Errorf("secret path logged as %q", got)
	}
}
