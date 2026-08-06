// Package swagger embeds the generated OpenAPI bundle so the API server can
// serve its own documentation without a second deployment artifact.
//
// The bundle is produced by protoc-gen-openapiv2 during `make proto` (see
// api/v1/gen.go) and is committed, so the binary is always self-describing.
package swagger

import "embed"

// OpenAPI holds the generated API documentation, served at /swagger/ by the
// backend's public listener.
//
//go:embed OpenAPI
var OpenAPI embed.FS
