// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "testing"

// TestAdminTenantAccessIsOffByDefault pins decision 41: an installation admin reaches a tenant user's account only
// when the operator turns admin.tenant_access on.
func TestAdminTenantAccessIsOffByDefault(t *testing.T) {
	t.Parallel()
	if Defaults().Admin.TenantAccess {
		t.Fatal("admin.tenant_access defaults to on; decision 41 says off")
	}
}
