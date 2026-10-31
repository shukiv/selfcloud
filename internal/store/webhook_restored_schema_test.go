// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "testing"

// file.restored is an event type an endpoint may subscribe to, and the schema accepts it.
func TestSubscriptionAcceptsFileRestored(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	ep := newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.restored")

	got, err := s.WebhookEndpoint(t.Context(), fx.acc, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0] != "file.restored" {
		t.Fatalf("events %v, want file.restored", got.Events)
	}
}
