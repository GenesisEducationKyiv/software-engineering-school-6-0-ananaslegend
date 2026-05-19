package emailer

import (
	"embed"
	htmltpl "html/template"
	texttpl "text/template"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

var (
	confirmationHTMLTmpl = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/confirmation.html"))
	confirmationTXTTmpl  = texttpl.Must(texttpl.ParseFS(templateFS, "templates/confirmation.txt"))
	releaseHTMLTmpl      = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/release.html"))
	releaseTXTTmpl       = texttpl.Must(texttpl.ParseFS(templateFS, "templates/release.txt"))
)
