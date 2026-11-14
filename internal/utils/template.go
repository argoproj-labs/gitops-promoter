package utils

import (
	"bytes"
	"fmt"
	"net/url"
	"text/template"

	sprig "github.com/go-task/slim-sprig/v3"
)

var sanitizedSprigFuncMap = sprig.GenericFuncMap()

func init() {
	delete(sanitizedSprigFuncMap, "env")
	delete(sanitizedSprigFuncMap, "expandenv")
	delete(sanitizedSprigFuncMap, "getHostByName")
	sanitizedSprigFuncMap["urlQueryEscape"] = url.QueryEscape
}

// RenderStringTemplate renders a string template with the provided data.
func RenderStringTemplate(templateStr string, data any, options ...string) (string, error) {
	tmpl, err := template.New("").Funcs(sanitizedSprigFuncMap).Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Apply options to the template
	for _, option := range options {
		tmpl = tmpl.Option(option)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// RenderURLTemplate renders a URL template with the provided data and checks that the result is an
// http or https URL, which is what the link of a commit status must be.
func RenderURLTemplate(templateStr string, data any, options ...string) (string, error) {
	rendered, err := RenderStringTemplate(templateStr, data, options...)
	if err != nil {
		return "", fmt.Errorf("failed to render URL template: %w", err)
	}
	parsed, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL scheme is not http or https: %s", parsed.Scheme)
	}
	return rendered, nil
}
