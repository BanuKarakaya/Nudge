package slack

import (
	_ "embed"
	"strings"
)

//go:embed manifest.json
var appManifest string

// Manifest receives the already validated canonical public origin.
func Manifest(origin string) string {
	return strings.ReplaceAll(appManifest, "https://YOUR_NUDGY_HOST", strings.TrimRight(origin, "/"))
}
