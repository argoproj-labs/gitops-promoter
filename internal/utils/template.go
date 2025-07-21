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
