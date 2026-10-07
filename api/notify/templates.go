package notify

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
)

//go:embed templates/*
var templateFS embed.FS

var (
	htmlLayout = htmltemplate.Must(htmltemplate.ParseFS(templateFS, "templates/layout.html"))
	textLayout = texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/layout.txt"))
)

type Email struct {
	To          string
	Subject     string
	Heading     string
	Paragraphs  []string
	ActionLabel string
	ActionURL   string
	Code        string
	Footnote    string
}

func (e Email) Render() (Message, error) {
	var html, text bytes.Buffer
	if err := htmlLayout.Execute(&html, e); err != nil {
		return Message{}, fmt.Errorf("render html: %w", err)
	}
	if err := textLayout.Execute(&text, e); err != nil {
		return Message{}, fmt.Errorf("render text: %w", err)
	}
	return Message{To: e.To, Subject: e.Subject, HTML: html.String(), Text: text.String()}, nil
}
