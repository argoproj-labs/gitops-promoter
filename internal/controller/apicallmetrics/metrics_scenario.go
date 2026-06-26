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

package apicallmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	testBranchDevelopment = "environment/development"
	testBranchStaging     = "environment/staging"
	testBranchProduction  = "environment/production"
)

// APICallMetricsScenario configures a git CLI metrics collection run.
type APICallMetricsScenario struct {
	Name                     string
	EnvironmentCount         int
	RequeueDuration          time.Duration
	TimedGateByBranch        map[string]time.Duration
	ArgoAppHealthyAfterByEnv map[string]time.Duration
	TriggerChange            bool
	PhaseSnapshots           bool
	FullGateStack            bool
	// SoakDuration runs a fixed wall-clock soak after PR posture is established (no full promotion).
	SoakDuration time.Duration
	// PRPosture is "open" or "closed" for soak scenarios (see metrics_soak.go).
	PRPosture string
	// DisableAutoMerge keeps promotion PRs open on the SCM during soak runs.
	DisableAutoMerge bool
}

const (
	argocdHealthKey = "argocd-health"
	timerKey        = "timer"
	devGCSKey       = "dev-gcs"
	stagingGCSKey   = "staging-gcs"
	prodGCSKey      = "prod-gcs"
	wrcsDevKey      = "wrcs-dev"
	wrcsStagingKey  = "wrcs-staging"
	wrcsProdKey     = "wrcs-prod"
)

func defaultFullGateScenario() APICallMetricsScenario {
	return APICallMetricsScenario{
		Name:                     "full_gate_stack_three_env",
		EnvironmentCount:         3,
		RequeueDuration:          metricsSuiteRequeueDuration(),
		TimedGateByBranch:        loadTimedGateByBranchFromEnv(),
		ArgoAppHealthyAfterByEnv: loadArgoAppHealthyAfterByEnvFromEnv(),
		TriggerChange:            true,
		PhaseSnapshots:           true,
		FullGateStack:            true,
	}
}

func configurePromotionStrategy(ps *promoterv1alpha1.PromotionStrategy, scenario APICallMetricsScenario) {
	if scenario.EnvironmentCount == 1 {
		ps.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
		}
	}
	if !scenario.FullGateStack {
		return
	}
	ps.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
		{Key: argocdHealthKey},
		{Key: timerKey},
	}
	if scenario.DisableAutoMerge {
		autoMerge := false
		for i := range ps.Spec.Environments {
			ps.Spec.Environments[i].AutoMerge = &autoMerge
		}
	}
	for i := range ps.Spec.Environments {
		env := &ps.Spec.Environments[i]
		switch env.Branch {
		case testBranchDevelopment:
			env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: devGCSKey}, {Key: wrcsDevKey},
			}
		case testBranchStaging:
			env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: stagingGCSKey}, {Key: wrcsStagingKey},
			}
		case testBranchProduction:
			env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: prodGCSKey}, {Key: wrcsProdKey},
			}
		}
	}
}

func newGitCommitStatus(name, psName, key string) *promoterv1alpha1.GitCommitStatus {
	return &promoterv1alpha1.GitCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.GitCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
			Key:                  key,
			Description:          "metrics gate " + key,
			Expression:           `Commit.Author != ""`,
			Target:               constants.CommitRefProposed,
		},
	}
}

func newApprovalWRCS(name, psName, key, serverURL string) *promoterv1alpha1.WebRequestCommitStatus {
	return &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
			Key:                  key,
			ReportOn:             constants.CommitRefProposed,
			HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
				URLTemplate:    serverURL + `/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
				MethodTemplate: "GET",
				Timeout:        metav1.Duration{Duration: 10 * time.Second},
			},
			Success: promoterv1alpha1.SuccessSpec{
				When: promoterv1alpha1.WhenWithOutputSpec{
					Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
				},
			},
			Mode: promoterv1alpha1.ModeSpec{
				Polling: &promoterv1alpha1.PollingModeSpec{
					Interval: metav1.Duration{Duration: 5 * time.Second},
				},
			},
		},
	}
}

func startApprovalHTTPServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approved": true,
			"status":   "approved",
		})
	}))
}

func newTimedCommitStatus(name, psName string, timedByBranch map[string]time.Duration) *promoterv1alpha1.TimedCommitStatus {
	// Stable branch order so spec matches PromotionStrategy env ordering in logs/tests.
	branches := []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
	envs := make([]promoterv1alpha1.TimedCommitStatusEnvironments, 0, len(timedByBranch))
	for _, branch := range branches {
		d, ok := timedByBranch[branch]
		if !ok {
			continue
		}
		envs = append(envs, promoterv1alpha1.TimedCommitStatusEnvironments{
			Branch:   branch,
			Duration: metav1.Duration{Duration: d},
		})
	}
	return &promoterv1alpha1.TimedCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.TimedCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
			Key:                  timerKey,
			Environments:         envs,
		},
	}
}

func newArgoCDCommitStatus(name, psName, appLabel string) *promoterv1alpha1.ArgoCDCommitStatus {
	return &promoterv1alpha1.ArgoCDCommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
			Key:                  argocdHealthKey,
			ApplicationSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": appLabel},
			},
		},
	}
}

func patchArgoAppSyncedHealthy(app *argocd.Application, hydratedSha string) {
	app.Status.Sync.Status = argocd.SyncStatusCodeSynced
	app.Status.Health.Status = argocd.HealthStatusHealthy
	app.Status.Sync.Revision = hydratedSha
	now := metav1.Now()
	app.Status.Health.LastTransitionTime = &now
}

func promotionStrategyResource(ctx context.Context, name, namespace string) (string, *corev1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PromotionStrategy) {
	stem := name + "-" + utils.KubeSafeUniqueName(randomString(15))
	secName := stem + "-sec"
	scmName := stem + "-scm"
	grName := stem + "-gr"
	psName := stem + "-ps"

	scmSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secName, Namespace: namespace},
	}
	scmProvider := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: scmName, Namespace: namespace},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &corev1.LocalObjectReference{Name: secName},
			Fake:      &promoterv1alpha1.Fake{},
		},
	}
	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: grName, Namespace: namespace},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Fake: &promoterv1alpha1.FakeRepo{Owner: grName, Name: grName},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: scmName,
			},
		},
	}
	promotionStrategy := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: psName, Namespace: namespace},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: grName},
			Environments: []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
				{Branch: testBranchStaging},
				{Branch: testBranchProduction},
			},
		},
	}
	_ = ctx
	return psName, scmSecret, scmProvider, gitRepo, promotionStrategy
}

func argocdApplications(namespace, appLabel, repoOwner, repoName string) (argocd.Application, argocd.Application, argocd.Application) {
	environments := []string{"development", "staging", "production"}
	apps := make([]argocd.Application, len(environments))
	for i, environment := range environments {
		apps[i] = argocd.Application{
			TypeMeta: metav1.TypeMeta{Kind: "Application", APIVersion: "argoproj.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      appLabel + "-" + environment,
				Namespace: namespace,
				Labels:    map[string]string{"app": appLabel},
			},
			Spec: argocd.ApplicationSpec{
				SourceHydrator: &argocd.SourceHydrator{
					DrySource: argocd.DrySource{
						RepoURL: fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoOwner, repoName),
					},
					SyncSource: argocd.SyncSource{TargetBranch: "environment/" + environment},
				},
			},
			Status: argocd.ApplicationStatus{
				Sync:   argocd.SyncStatus{Status: argocd.SyncStatusCodeOutOfSync},
				Health: argocd.HealthStatus{Status: argocd.HealthStatusHealthy, LastTransitionTime: &metav1.Time{Time: time.Now()}},
			},
		}
	}
	return apps[0], apps[1], apps[2]
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}
