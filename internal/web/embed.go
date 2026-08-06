// Package web embeds the compiled frontend so the production binary is fully
// self-contained: the Jetson never needs Node.js, and deployment is one file.
package web

import (
	"embed"
	"errors"
	"io/fs"
)

// dist holds the Vite build output. The `all:` prefix keeps files whose names
// begin with an underscore or dot, which Vite emits for chunked assets.
//
// The placeholder file guarantees this compiles before the frontend is built;
// `make frontend` replaces the directory contents.
//
//go:embed all:dist
var dist embed.FS

// ErrNotBuilt indicates the frontend has not been compiled into the binary.
var ErrNotBuilt = errors.New("web: frontend assets are not built; run `make frontend`")

// Assets returns the embedded frontend filesystem rooted at dist/.
func Assets() (fs.FS, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	// A build that produced no index.html is a broken build, not an empty one;
	// failing loudly at startup beats serving 404s to a wall-mounted display.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, ErrNotBuilt
	}
	return sub, nil
}

// IsBuilt reports whether real frontend assets are embedded.
func IsBuilt() bool {
	_, err := Assets()
	return err == nil
}
