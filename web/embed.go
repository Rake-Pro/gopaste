// Package web embeds the frontend asset bundle so gopaste ships as a single
// binary. The backend depends only on the HTTP wire contract, not on the
// contents of these assets, so the bundle can be swapped during the planned
// UI redesign without backend changes.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

//go:embed about.md
var aboutMD []byte

// AboutMD returns the embedded "about" document, preloaded into the store under
// the "about" key as a built-in help page.
func AboutMD() []byte { return aboutMD }

// Static returns the embedded static asset filesystem rooted at the static
// directory (so paths are served as "/application.js", not "/static/...").
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// staticFS is compiled in; a failure here is a build-time mistake.
		panic("web: embed static sub: " + err.Error())
	}
	return sub
}
