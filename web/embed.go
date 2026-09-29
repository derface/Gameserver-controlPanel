// Package web embeds the browser UI into the binary.
package web

import "embed"

//go:embed static
var FS embed.FS
