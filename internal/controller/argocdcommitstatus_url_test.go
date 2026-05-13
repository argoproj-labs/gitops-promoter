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

package controller

import "testing"

func TestValidateRenderedSCMURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "absolute https", url: "https://argocd.example.com/applications/foo", wantErr: false},
		{name: "absolute http", url: "http://argocd.example.com/applications/foo", wantErr: false},
		{name: "absolute https with query", url: "https://argocd.example.com/applications?labels=team%3Dfoo", wantErr: false},

		{name: "root-relative rejected", url: "/applications?labels=team%3Dfoo", wantErr: true},
		{name: "root-relative root only rejected", url: "/", wantErr: true},
		{name: "ftp scheme rejected", url: "ftp://example.com/x", wantErr: true},
		{name: "javascript scheme rejected", url: "javascript:alert(1)", wantErr: true},
		{name: "protocol-relative rejected", url: "//evil.example.com/x", wantErr: true},
		{name: "bare relative path rejected", url: "applications/foo", wantErr: true},
		{name: "empty string rejected", url: "", wantErr: true},
		{name: "malformed rejected", url: "http://[::1", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateRenderedSCMURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRenderedSCMURL(%q) error = %v, wantErr = %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestValidateRenderedDetailURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "absolute https", url: "https://argocd.example.com/applications/foo", wantErr: false},
		{name: "absolute http", url: "http://argocd.example.com/applications/foo", wantErr: false},
		{name: "absolute https with query", url: "https://argocd.example.com/applications?labels=team%3Dfoo", wantErr: false},
		{name: "root-relative with query", url: "/applications?labels=team%3Dfoo", wantErr: false},
		{name: "root-relative root only", url: "/", wantErr: false},
		{name: "root-relative deep path", url: "/applications/argo/my-app", wantErr: false},

		{name: "ftp scheme rejected", url: "ftp://example.com/x", wantErr: true},
		{name: "javascript scheme rejected", url: "javascript:alert(1)", wantErr: true},
		{name: "protocol-relative rejected", url: "//evil.example.com/x", wantErr: true},
		{name: "bare relative path rejected", url: "applications/foo", wantErr: true},
		{name: "empty string rejected", url: "", wantErr: true},
		{name: "malformed rejected", url: "http://[::1", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateRenderedDetailURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRenderedDetailURL(%q) error = %v, wantErr = %v", tc.url, err, tc.wantErr)
			}
		})
	}
}
