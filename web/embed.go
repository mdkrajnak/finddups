package web

import "embed"

// StaticFiles embeds all files in the static directory into the binary.
// This allows the web server to serve HTML, CSS, and JS files without
// requiring external file dependencies at runtime.
//
//go:embed static/*
var StaticFiles embed.FS

// TemplateFiles embeds all template files for server-side rendering.
//
//go:embed templates/*.html
var TemplateFiles embed.FS
