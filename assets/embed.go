// Package assets holds everything compiled into the binary: the goose
// migrations applied at boot, and the static files served under
// /static. Nothing here needs to exist on the host at runtime.
package assets

import (
	"embed"
)

//go:embed migrations static
var EmbeddedFiles embed.FS
