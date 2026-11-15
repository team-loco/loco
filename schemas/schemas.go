// Package schemas embeds the JSON Schemas Loco publishes for its configuration files.
package schemas

import _ "embed"

// LocoV1 is the JSON Schema of loco.yaml version 1.
//
//go:embed loco.v1.json
var LocoV1 []byte
