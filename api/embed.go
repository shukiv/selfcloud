// Package api embeds the OpenAPI document, so the server can publish it and tests can check
// the routes against it.
package api

import _ "embed"

// Spec is api/openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
