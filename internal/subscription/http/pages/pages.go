package pages

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*
var templateFS embed.FS

var (
	confirmedTmpl    = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/confirmed.html"))
	unsubscribedTmpl = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/unsubscribed.html"))
	unavailableTmpl  = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/unavailable.html"))
	oopsTmpl         = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/oops.html"))
	landingTmpl      = template.Must(template.ParseFS(templateFS, "templates/landing.html"))
	subscribedTmpl   = template.Must(template.ParseFS(templateFS, "templates/subscribed.html"))
)

// Renderer renders HTML response pages. Zero-value is ready to use.
type Renderer struct{}

func (r Renderer) Confirmed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = confirmedTmpl.ExecuteTemplate(w, "base", nil)
}

func (r Renderer) Unsubscribed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = unsubscribedTmpl.ExecuteTemplate(w, "base", nil)
}

// Unavailable renders the "link expired or not found" page.
// status should be http.StatusNotFound (404) or http.StatusGone (410).
func (r Renderer) Unavailable(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = unavailableTmpl.ExecuteTemplate(w, "base", nil)
}

type oopsData struct {
	RequestID string
}

func (r Renderer) Oops(w http.ResponseWriter, requestID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = oopsTmpl.ExecuteTemplate(w, "base", oopsData{RequestID: requestID})
}

func (r Renderer) Landing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = landingTmpl.Execute(w, nil)
}

func (r Renderer) Subscribed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = subscribedTmpl.Execute(w, nil)
}
