package email

import (
	"bytes"
	"embed"
	"fmt"
	htmltpl "html/template"
	texttpl "text/template"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

var (
	confirmationHTMLTmpl = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/confirmation.html"))
	confirmationTXTTmpl  = texttpl.Must(texttpl.ParseFS(templateFS, "templates/confirmation.txt"))
	releaseHTMLTmpl      = htmltpl.Must(htmltpl.ParseFS(templateFS, "templates/release.html"))
	releaseTXTTmpl       = texttpl.Must(texttpl.ParseFS(templateFS, "templates/release.txt"))
)

type renderedEmail struct {
	Subject string
	HTML    string
	Text    string
}

func renderConfirmation(p contract.SendConfirmationRequest) (renderedEmail, error) {
	html, text, err := renderHTMLAndText(confirmationHTMLTmpl, confirmationTXTTmpl, p)
	if err != nil {
		return renderedEmail{}, fmt.Errorf("email.renderConfirmation: %w", err)
	}
	return renderedEmail{
		Subject: fmt.Sprintf("Confirm your subscription to %s", p.RepoFullName),
		HTML:    html,
		Text:    text,
	}, nil
}

func renderRelease(p contract.SendReleaseRequest) (renderedEmail, error) {
	html, text, err := renderHTMLAndText(releaseHTMLTmpl, releaseTXTTmpl, p)
	if err != nil {
		return renderedEmail{}, fmt.Errorf("email.renderRelease: %w", err)
	}
	return renderedEmail{
		Subject: fmt.Sprintf("New release %s for %s", p.ReleaseTag, p.RepoFullName),
		HTML:    html,
		Text:    text,
	}, nil
}

func renderHTMLAndText(htmlTmpl *htmltpl.Template, textTmpl *texttpl.Template, data any) (string, string, error) {
	var htmlBuf, textBuf bytes.Buffer
	if err := htmlTmpl.Execute(&htmlBuf, data); err != nil {
		return "", "", fmt.Errorf("render html: %w", err)
	}
	if err := textTmpl.Execute(&textBuf, data); err != nil {
		return "", "", fmt.Errorf("render text: %w", err)
	}
	return htmlBuf.String(), textBuf.String(), nil
}
