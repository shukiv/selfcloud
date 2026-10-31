// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/netip"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// stubResolver answers the host names a test uses, so no test looks up DNS. A name it does not hold resolves to a public
// address, so an ordinary registration passes. A name mapped to no address does not resolve.
type stubResolver struct {
	hosts map[string][]netip.Addr
}

// errNoSuchHost is the lookup failure of a name the stub maps to no address.
var errNoSuchHost = errors.New("no such host")

// publicAddr is a public address (example.com) that every name resolves to by default.
var publicAddr = netip.MustParseAddr("93.184.216.34")

func (r stubResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := r.hosts[host]; ok {
		if len(a) == 0 {
			return nil, errNoSuchHost
		}
		return a, nil
	}
	return []netip.Addr{publicAddr}, nil
}

// testWebhookClient is the client a test server uses: the stub resolver, and the networks the test configured.
func testWebhookClient(cfg *config.Config, hosts map[string][]netip.Addr) *webhook.Client {
	allow, _ := config.ParsePrefixes(cfg.Webhooks.AllowNetworks)
	return webhook.NewClient(webhook.ClientOptions{Resolver: stubResolver{hosts: hosts}, Allow: allow,
		AllowHTTP: cfg.Webhooks.AllowHTTP})
}
