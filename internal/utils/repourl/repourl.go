/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package repourl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ConvertToWebURL normalizes a git remote URL to an HTTPS base URL suitable for /commit/{sha} links.
// Strips .git suffix and embedded credentials. Handles https, git@host:path, and ssh:// forms.
func ConvertToWebURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty repo URL")
	}

	normalized := raw
	if strings.HasPrefix(normalized, "git@") {
		converted, err := scpStyleToHTTPS(normalized)
		if err != nil {
			return "", err
		}
		normalized = converted
	} else if strings.HasPrefix(normalized, "ssh://") {
		converted, err := sshURLToHTTPS(normalized)
		if err != nil {
			return "", err
		}
		normalized = converted
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("parse repo URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported repo URL scheme %q in %q", u.Scheme, raw)
	}

	u.Scheme = "https"
	u.User = nil
	u.Fragment = ""
	u.RawQuery = ""

	result := strings.TrimSuffix(u.String(), "/")
	result = strings.TrimSuffix(result, ".git")
	return result, nil
}

func scpStyleToHTTPS(raw string) (string, error) {
	rest := strings.TrimPrefix(raw, "git@")
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return "", fmt.Errorf("invalid scp-style repo URL: %q", raw)
	}
	host := rest[:colon]
	path := strings.TrimPrefix(rest[colon+1:], "/")
	return "https://" + host + "/" + path, nil
}

func sshURLToHTTPS(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse ssh repo URL %q: %w", raw, err)
	}
	host := u.Host
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	path := strings.TrimPrefix(u.Path, "/")
	return "https://" + host + "/" + path, nil
}
