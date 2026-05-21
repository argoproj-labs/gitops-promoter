/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRenderAndResolveCommitStatusURL(t *testing.T) {
	t.Parallel()

	data := URLTemplateData{
		Environment: "environment/staging",
		ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		},
	}

	tests := []struct {
		name          string
		template      string
		argoCDBaseURL string
		wantURL       string
		wantOK        bool
		wantErr       bool
	}{
		{
			name:          "absolute https template passes through unchanged",
			template:      "https://argocd.example.com/applications?env={{.Environment}}",
			argoCDBaseURL: "https://ignored.example.com",
			wantURL:       "https://argocd.example.com/applications?env=environment/staging",
			wantOK:        true,
		},
		{
			name:          "absolute http template passes through",
			template:      "http://argocd.local/x",
			argoCDBaseURL: "",
			wantURL:       "http://argocd.local/x",
			wantOK:        true,
		},
		{
			name:          "root-relative template with base URL is prepended",
			template:      "/applications?env={{.Environment}}",
			argoCDBaseURL: "https://argocd.example.com",
			wantURL:       "https://argocd.example.com/applications?env=environment/staging",
			wantOK:        true,
		},
		{
			name:          "root-relative template with no base URL returns ok=false",
			template:      "/applications",
			argoCDBaseURL: "",
			wantURL:       "",
			wantOK:        false,
		},
		{
			name:          "leading/trailing whitespace is trimmed before prepend",
			template:      "  /applications  ",
			argoCDBaseURL: "https://argocd.example.com",
			wantURL:       "https://argocd.example.com/applications",
			wantOK:        true,
		},
		{
			name:          "protocol-relative URL is rejected",
			template:      "//evil.example.com/x",
			argoCDBaseURL: "https://argocd.example.com",
			wantErr:       true,
		},
		{
			name:          "ftp scheme is rejected",
			template:      "ftp://example.com/x",
			argoCDBaseURL: "",
			wantErr:       true,
		},
		{
			name:          "rendered value with no scheme and no leading slash is rejected",
			template:      "applications",
			argoCDBaseURL: "https://argocd.example.com",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotURL, gotOK, err := renderAndResolveCommitStatusURL(tt.template, nil, data, tt.argoCDBaseURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if gotOK != tt.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotURL != tt.wantURL {
				t.Errorf("url = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}

func TestResolveArgoCDBaseURL(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := promoterv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add promoter scheme: %v", err)
	}

	makeCM := func(url string) *corev1.ConfigMap {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      argoCDConfigMapName,
				Namespace: argoCDConfigMapNamespace,
			},
		}
		if url != "" {
			cm.Data = map[string]string{argoCDConfigMapURLKey: url}
		}
		return cm
	}

	tests := []struct {
		name         string
		override     string
		configMapURL string
		wantBaseURL  string
		hasConfigMap bool
		wantErr      bool
	}{
		{
			name:         "override wins over configmap",
			override:     "https://override.example.com",
			configMapURL: "https://argocd-cm.example.com",
			wantBaseURL:  "https://override.example.com",
			hasConfigMap: true,
		},
		{
			name:        "trailing slash on override is trimmed",
			override:    "https://override.example.com/",
			wantBaseURL: "https://override.example.com",
		},
		{
			name:         "configmap url used when no override",
			configMapURL: "https://argocd-cm.example.com",
			wantBaseURL:  "https://argocd-cm.example.com",
			hasConfigMap: true,
		},
		{
			name:         "trailing slash on configmap value is trimmed",
			configMapURL: "https://argocd-cm.example.com/",
			wantBaseURL:  "https://argocd-cm.example.com",
			hasConfigMap: true,
		},
		{
			name:         "configmap present but url key empty falls through to empty",
			configMapURL: "",
			wantBaseURL:  "",
			hasConfigMap: true,
		},
		{
			name:        "no override and no configmap returns empty without error",
			wantBaseURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			objs := []client.Object{}
			if tt.hasConfigMap {
				objs = append(objs, makeCM(tt.configMapURL))
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			r := &ArgoCDCommitStatusReconciler{localClient: c}
			acs := &promoterv1alpha1.ArgoCDCommitStatus{
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					ArgoCDBaseURL: tt.override,
				},
			}

			got, err := r.resolveArgoCDBaseURL(context.Background(), acs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", got, tt.wantBaseURL)
			}
		})
	}
}
