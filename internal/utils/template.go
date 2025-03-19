package utils

import (
	"bytes"
	"fmt"
	"text/template"

	sprig "github.com/go-task/slim-sprig/v3"
)

var sanitizedSprigFuncMap = sprig.GenericFuncMap()

func init() {
	delete(sanitizedSprigFuncMap, "env")
	delete(sanitizedSprigFuncMap, "expandenv")
	delete(sanitizedSprigFuncMap, "getHostByName")
}

func RenderStringTemplate(templateStr string, data any) (string, error) {
	tmpl, err := template.New("").Funcs(sanitizedSprigFuncMap).Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
