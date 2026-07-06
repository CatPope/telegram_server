// Package templates embeds the admin UI's html/template files into the
// binary so the UI ships as a single self-contained executable.
package templates

import (
	"embed"
	"html/template"
)

//go:embed *.html
var FS embed.FS

// ParsePage parses base.html together with a single page template. Pages
// are parsed pairwise (rather than all at once) so each can independently
// define a "content" block without name collisions across pages.
func ParsePage(page string) (*template.Template, error) {
	return template.ParseFS(FS, "base.html", page)
}
