package dashboard

import (
	"embed"
	"io/fs"
)

// Files contains the buildless dashboard assets so the Go binary can serve
// the dashboard in the same container as the Slack webhook.
//
//go:embed static/*
var files embed.FS

func Files() fs.FS {
	root, _ := fs.Sub(files, "static")
	return root
}
