// Package web embeds the self-contained single-page admin UI and exposes it
// as an fs.FS so the api package can serve it without any build step.
package web

import (
	"embed"
	"io/fs"
)

// dist holds the built (here: hand-authored, no-build) SPA assets.
//
//go:embed dist
var dist embed.FS

// FS returns the SPA file tree rooted at the dist directory, so that
// "index.html", "app.js" and "style.css" are reachable at the FS root.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// dist is embedded at compile time; a failure here is a programmer
		// error (renamed/removed directory) and can never happen at runtime.
		panic(err)
	}
	return sub
}
