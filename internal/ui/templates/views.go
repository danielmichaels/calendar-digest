// Package templates holds the templ components and the view models they
// render.
//
// View models live in this plain Go file, not in a .templ file: only the
// generated _templ.go is compiled, so a type declared in a .templ would not
// exist until `task templ` had run.
package templates

type HomeView struct {
	Title string
	// Flash is a one-shot message surviving exactly one render.
	Flash string
}
