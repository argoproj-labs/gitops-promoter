package web

import "embed"

//go:embed static/* static/assets/*
var DashboardFS embed.FS
