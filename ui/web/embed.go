package web

import "embed"

// DashboardFS contains the embedded dashboard static files.
//
//go:embed static
var DashboardFS embed.FS
