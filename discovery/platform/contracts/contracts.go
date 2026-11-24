// Package contracts embeds the API contract so the binary can serve it and
// tests can check the implementation against it.
package contracts

import _ "embed"

// OpenAPI is the OpenAPI 3.1 document for /api/v1.
//
//go:embed openapi.yaml
var OpenAPI []byte
