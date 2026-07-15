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

import (
	"strings"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolvePreviousEnvironmentURL(t *testing.T) {
	ps := promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocon-demo",
			Namespace: "promoter-ns",
		},
	}
	tests := []struct {
		name    string
		url     promoterv1alpha1.PreviousEnvironmentURLConfig
		phases  []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase
		want    string
		wantErr string
	}{
		{
			name: "template renders using template data",
			url: promoterv1alpha1.PreviousEnvironmentURLConfig{
				Template: `https://argocd.local/applications/{{ .PromotionStrategy.Namespace }}/{{ .PromotionStrategy.Name }}?branch={{ .PreviousEnvironmentBranch }}&sha={{ .Sha }}`,
			},
			want: "https://argocd.local/applications/promoter-ns/argocon-demo?branch=environments/staging&sha=abc123",
		},
		{
			name: "template overrides the single-status fallback",
			url: promoterv1alpha1.PreviousEnvironmentURLConfig{
				Template: "https://argocd.local",
			},
			phases: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "check-1", Phase: "success", Url: "https://ignored.local"},
			},
			want: "https://argocd.local",
		},
		{
			name: "template uses aggregated statuses",
			url: promoterv1alpha1.PreviousEnvironmentURLConfig{
				Template: `https://argocd.local/{{ (index .AggregatedStatuses 0).Key }}`,
			},
			phases: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "check-1", Phase: "success"},
			},
			want: "https://argocd.local/check-1",
		},
		{
			name:   "no template, exactly one status uses that status url",
			phases: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{{Key: "check-1", Phase: "success", Url: "https://check-1.local"}},
			want:   "https://check-1.local",
		},
		{
			name: "no template, multiple statuses yields empty url",
			phases: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "check-1", Phase: "success", Url: "https://check-1.local"},
				{Key: "check-2", Phase: "success", Url: "https://check-2.local"},
			},
			want: "",
		},
		{
			name:   "no template, zero statuses yields empty url",
			phases: nil,
			want:   "",
		},
		{
			name: "template rendering non-http(s) scheme is rejected",
			url: promoterv1alpha1.PreviousEnvironmentURLConfig{
				Template: "ftp://argocd.local",
			},
			wantErr: "scheme is not http or https",
		},
		{
			name: "invalid template returns a render error",
			url: promoterv1alpha1.PreviousEnvironmentURLConfig{
				Template: "{{ .DoesNotExist ",
			},
			wantErr: "failed to render previous environment URL template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := PreviousEnvironmentURLTemplateData{
				PromotionStrategy:         ps,
				PreviousEnvironmentBranch: "environments/staging",
				Sha:                       "abc123",
				AggregatedStatuses:        tt.phases,
			}

			got, err := resolvePreviousEnvironmentURL(tt.url, data, tt.phases)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (url=%q)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got url %q, want %q", got, tt.want)
			}
		})
	}
}
