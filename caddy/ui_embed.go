package caddyprometheus

import (
	"embed"
	"net/http"

	"github.com/prometheus/common/assets"
	"github.com/prometheus/prometheus/web/ui"
)

//go:embed static
var staticFS embed.FS

func init() {
	// Embed Prometheus' ui.Assets path to serve from memory
	// Regenerate after a Prometheus version bump: go generate ./caddy/...
	ui.Assets = http.FS(assets.New(staticFS))
}
