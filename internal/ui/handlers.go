package ui

import (
	"net/http"

	"github.com/danielmichaels/calendar-digest/internal/ui/templates"
)

func (h *Handlers) handleHome(w http.ResponseWriter, r *http.Request) {
	view := templates.HomeView{
		Title: "calendar digest",
		Flash: h.Sessions.PopString(r.Context(), flashKey),
	}
	if err := templates.Home(view).Render(r.Context(), w); err != nil {
		h.serverError(w, r, err, "render home")
	}
}
