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

// ValidateHTTPURL parses a rendered URL template and rejects anything an SCM will not render as a
// details link. Commit status URLs come from user-supplied templates, so a template that renders a
// javascript:, file: or relative URL must fail the reconcile rather than reach the SCM.
func ValidateHTTPURL(rendered string) error {
	parsedURL, err := url.Parse(rendered)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL scheme is not http or https: %s", parsedURL.Scheme)
	}
	return nil
}
