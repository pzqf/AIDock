// Package schemas embeds versioned JSON Schemas without fetching remote refs.
package schemas

import "embed"

//go:embed *.schema.json
var Files embed.FS
