// Package web embeds the amberFOCUS Setup browser UI.
package web

import "embed"

// FS holds the embedded static assets (index.html).
//
//go:embed index.html
var FS embed.FS
