package scmserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Timestamp formats used by each provider's JSON APIs.
const (
	githubTimeFormat    = time.RFC3339
	gitlabTimeFormat    = "2006-01-02T15:04:05.000Z"
	azureTimeFormat     = "2006-01-02T15:04:05.0000000Z"
	bitbucketTimeFormat = "2006-01-02T15:04:05.000000+00:00"
	giteaTimeFormat     = time.RFC3339
)

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeNoContent writes an empty response with Content-Length 0, which every provider SDK accepts.
func writeNoContent(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(status)
}

// writeProviderError writes an error in the provider's native error format.
func writeProviderError(w http.ResponseWriter, p Provider, status int, message string) {
	if message == "" {
		message = http.StatusText(status)
	}
	switch p {
	case GitHub:
		writeJSON(w, status, map[string]any{
			"message":           message,
			"documentation_url": "https://docs.github.com/enterprise-server@3.14/rest",
			"status":            strconv.Itoa(status),
		})
	case GitLab:
		writeJSON(w, status, map[string]any{"message": message})
	case AzureDevOps:
		writeJSON(w, status, azureError(message, "", 0))
	case BitbucketCloud:
		writeJSON(w, status, map[string]any{"type": "error", "error": map[string]any{"message": message}})
	case Gitea, Forgejo:
		writeJSON(w, status, map[string]any{"message": message, "url": "/api/swagger"})
	default:
		http.Error(w, message, status)
	}
}

// azureError is the Azure DevOps error body. typeKey and errorCode identify the server exception.
func azureError(message, typeKey string, errorCode int) map[string]any {
	if typeKey == "" {
		typeKey = "VssServiceException"
	}
	return map[string]any{
		"$id":            "1",
		"innerException": nil,
		"message":        message,
		"typeName":       "Microsoft.VisualStudio.Services.Common." + typeKey + ", Microsoft.VisualStudio.Services.Common",
		"typeKey":        typeKey,
		"errorCode":      errorCode,
		"eventId":        3000,
	}
}

// decodeJSON decodes the request body into v. It returns false and writes a provider-native 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, p Provider, v any) bool {
	if r.Body == nil {
		writeProviderError(w, p, http.StatusBadRequest, "Problems parsing JSON")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeProviderError(w, p, http.StatusBadRequest, "Problems parsing JSON: "+err.Error())
		return false
	}
	return true
}

func formatTime(t time.Time, layout string) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(layout)
}
