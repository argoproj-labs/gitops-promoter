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
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The gates a load fixture can be given, and the keys they report under.
const (
	loadGitGate       = "git-check"
	loadScheduledGate = "promotion-window"
	loadArgoCDGate    = "argocd-health"
	loadWebGate       = "github-checks"

	// loadWebRequestPending never succeeds: no check run called "ci" exists. loadWebRequestPassing succeeds on any
	// answer. Both keep the previous phase on a reconcile that sends no request.
	loadWebRequestPending = `Response != nil ? (Response.StatusCode == 200 && any(Response.Body.check_runs, {.name == "ci" && .conclusion == "success"})) : Phase == "success"`
	loadWebRequestPassing = `Response != nil ? Response.StatusCode == 200 : Phase == "success"`
)

// loadWebRequest describes a WebRequestCommitStatus that asks the GitHub API for the check runs of a commit, with
// the ScmProvider's credentials, the way the documentation's "GitHub required statuses" example does.
type loadWebRequest struct {
	// reportOn is the commit the status is reported on: constants.CommitRefProposed or constants.CommitRefActive.
	reportOn string
	// context is whether a request is made per environment or once for the PromotionStrategy.
	context promoterv1alpha1.ContextMode
	// expression decides success: loadWebRequestPending or loadWebRequestPassing.
	expression string
	// interval is the polling interval, or the requeue interval in trigger mode.
	interval time.Duration
	// trigger sends a request once per proposed commit instead of polling.
	trigger bool
}

// describe renders the WebRequestCommitStatus for the report.
func (w *loadWebRequest) describe() string {
	mode := "polling"
	if w.trigger {
		mode = "trigger mode (one request per proposed commit)"
	}
	outcome := "never succeeds"
	if w.expression == loadWebRequestPassing {
		outcome = "succeeds"
	}
	return fmt.Sprintf("%s, %s context, reported on the %s commit, %s", mode, w.context, w.reportOn, outcome)
}

// pollInterval is the fixture's WebRequestCommitStatus interval, or zero without one.
func (f *loadFixture) pollInterval() scmserver.Duration {
	if f.opts.webRequest == nil {
		return scmserver.Duration{}
	}
	return scmserver.D(f.opts.webRequest.interval)
}

// gateNames lists the fixture's gates for the report.
func (f *loadFixture) gateNames() []string {
	var gates []string
	if f.opts.manualGate {
		gates = append(gates, "manual CommitStatus (proposed)")
	}
	if f.opts.timedGate > 0 {
		gates = append(gates, fmt.Sprintf("TimedCommitStatus %s (active)", f.opts.timedGate))
	}
	if f.opts.gitGate != "" {
		gates = append(gates, "GitCommitStatus (proposed)")
	}
	if f.opts.scheduledGate != "" {
		gates = append(gates, fmt.Sprintf("ScheduledCommitStatus, window %s (proposed)", f.opts.scheduledGate))
	}
	if f.opts.argoCD {
		gates = append(gates, "ArgoCDCommitStatus, one Application per environment (active)")
	}
	if f.opts.webRequest != nil {
		gates = append(gates, "WebRequestCommitStatus ("+f.opts.webRequest.reportOn+")")
	}
	return gates
}

// addGateKeys declares the fixture's gates on a PromotionStrategy.
func (f *loadFixture) addGateKeys(ps *promoterv1alpha1.PromotionStrategy) {
	proposed := func(key string) {
		ps.Spec.ProposedCommitStatuses = append(ps.Spec.ProposedCommitStatuses, promoterv1alpha1.CommitStatusSelector{Key: key})
	}
	active := func(key string) {
		ps.Spec.ActiveCommitStatuses = append(ps.Spec.ActiveCommitStatuses, promoterv1alpha1.CommitStatusSelector{Key: key})
	}
	if f.opts.manualGate {
		proposed(loadManualGate)
	}
	if f.opts.gitGate != "" {
		proposed(loadGitGate)
	}
	if f.opts.scheduledGate != "" {
		proposed(loadScheduledGate)
	}
	if f.opts.timedGate > 0 {
		active(promoterv1alpha1.TimedCommitStatusDefaultKey)
	}
	if f.opts.argoCD {
		active(loadArgoCDGate)
	}
	if w := f.opts.webRequest; w != nil {
		if w.reportOn == constants.CommitRefActive {
			active(loadWebGate)
		} else {
			proposed(loadWebGate)
		}
	}
	if f.opts.labels != "" {
		ps.Spec.PullRequest = &promoterv1alpha1.PullRequestPolicySpec{
			Labels: &promoterv1alpha1.ScmLabelsSpec{Expression: f.opts.labels},
		}
	}
}

// gateObjects returns the gate resources of the i-th strategy.
func (f *loadFixture) gateObjects(i int) []client.Object {
	name := f.strategyName(i)
	ref := promoterv1alpha1.ObjectReference{Name: name}
	var objs []client.Object
	if f.opts.timedGate > 0 {
		timed := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec: promoterv1alpha1.TimedCommitStatusSpec{
				PromotionStrategyRef: ref,
				Key:                  promoterv1alpha1.TimedCommitStatusDefaultKey,
			},
		}
		for _, env := range f.envs {
			timed.Spec.Environments = append(timed.Spec.Environments, promoterv1alpha1.TimedCommitStatusEnvironments{
				Branch: env, Duration: metav1.Duration{Duration: f.opts.timedGate},
			})
		}
		objs = append(objs, timed)
	}
	if f.opts.gitGate != "" {
		objs = append(objs, &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec: promoterv1alpha1.GitCommitStatusSpec{
				PromotionStrategyRef: ref,
				Key:                  loadGitGate,
				Description:          "Checked by the load harness",
				Target:               constants.CommitRefProposed,
				Expression:           f.opts.gitGate,
			},
		})
	}
	if f.opts.scheduledGate != "" {
		scheduled := &promoterv1alpha1.ScheduledCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec:       promoterv1alpha1.ScheduledCommitStatusSpec{PromotionStrategyRef: ref, Key: loadScheduledGate},
		}
		always := []promoterv1alpha1.CronWindow{{Cron: "* * * * *", Duration: metav1.Duration{Duration: 24 * time.Hour}}}
		for _, env := range f.envs {
			window := promoterv1alpha1.ScheduledEnvironment{Branch: env, Allow: always}
			if f.opts.scheduledGate == loadWindowClosed {
				// An exclusion wins over the allowed window, so the window never opens.
				window.Exclude = always
			}
			scheduled.Spec.Environments = append(scheduled.Spec.Environments, window)
		}
		objs = append(objs, scheduled)
	}
	if f.opts.argoCD {
		objs = append(objs, &promoterv1alpha1.ArgoCDCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
				PromotionStrategyRef: ref,
				Key:                  loadArgoCDGate,
				ApplicationSelector:  &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			},
		})
		for e, env := range f.envs {
			objs = append(objs, f.application(i, e, env))
		}
	}
	if w := f.opts.webRequest; w != nil {
		objs = append(objs, f.webRequestCommitStatus(i, w))
	}
	return objs
}

// application builds the Argo CD Application of an environment. It starts out of sync, as an Application does
// until Argo CD has deployed the environment branch's head.
func (f *loadFixture) application(i, e int, env string) *argocd.Application {
	return &argocd.Application{
		TypeMeta: metav1.TypeMeta{Kind: "Application", APIVersion: "argoproj.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%d", f.strategyName(i), e), Namespace: loadNamespace,
			Labels: map[string]string{"app": f.strategyName(i)},
		},
		Spec: argocd.ApplicationSpec{
			SourceHydrator: &argocd.SourceHydrator{
				DrySource:  argocd.DrySource{RepoURL: f.repos[i].CloneURL},
				SyncSource: argocd.SyncSource{TargetBranch: env},
			},
		},
		Status: argocd.ApplicationStatus{
			Sync:   argocd.SyncStatus{Status: argocd.SyncStatusCodeOutOfSync},
			Health: argocd.HealthStatus{Status: argocd.HealthStatusProgressing},
		},
	}
}

// webRequestCommitStatus builds the WebRequestCommitStatus of the i-th strategy.
func (f *loadFixture) webRequestCommitStatus(i int, w *loadWebRequest) *promoterv1alpha1.WebRequestCommitStatus {
	name := f.strategyName(i)
	commit := "Proposed"
	if w.reportOn == constants.CommitRefActive {
		commit = "Active"
	}
	// The commit of the environment being checked, or of the first one when the request is made once for the
	// whole PromotionStrategy.
	sha := fmt.Sprintf(`{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .%s.Hydrated.Sha }}{{ end }}{{ end }}`, commit)
	if w.context == promoterv1alpha1.ContextPromotionStrategy {
		sha = fmt.Sprintf(`{{ (index .PromotionStrategy.Status.Environments 0).%s.Hydrated.Sha }}`, commit)
	}
	wrcs := &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
			Key:                  loadWebGate,
			ReportOn:             w.reportOn,
			HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
				URLTemplate: fmt.Sprintf("https://%s/api/v3/repos/%s/%s/commits/%s/check-runs?filter=latest",
					scmTarget.domain, f.repos[i].Owner, f.repos[i].Name, sha),
				MethodTemplate:  "GET",
				HeaderTemplates: map[string]string{"Accept": "application/vnd.github+json"},
				Timeout:         metav1.Duration{Duration: 10 * time.Second},
				Authentication:  &promoterv1alpha1.HTTPAuthentication{Scm: &promoterv1alpha1.Scm{}},
			},
			Success: promoterv1alpha1.SuccessSpec{When: promoterv1alpha1.WhenWithOutputSpec{Expression: w.expression}},
			Mode:    promoterv1alpha1.ModeSpec{Context: w.context},
		},
	}
	if w.trigger {
		const proposedSHA = `find(PromotionStrategy.Status.Environments, {.Branch == Branch}).Proposed.Hydrated.Sha`
		wrcs.Spec.Mode.Trigger = &promoterv1alpha1.TriggerModeSpec{
			RequeueDuration: metav1.Duration{Duration: w.interval},
			When: promoterv1alpha1.WhenWithOutputSpec{
				Expression: proposedSHA + ` != (TriggerOutput["trackedSha"] ?? "")`,
				Output:     &promoterv1alpha1.OutputSpec{Expression: `{ trackedSha: ` + proposedSHA + ` }`},
			},
		}
	} else {
		wrcs.Spec.Mode.Polling = &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: w.interval}}
	}
	return wrcs
}

// syncApplications plays Argo CD: it marks the Application of every environment synced to the environment
// branch's head and healthy, except for the environments the fixture keeps degraded.
func (f *loadFixture) syncApplications(ctx context.Context) {
	if !f.opts.argoCD {
		return
	}
	for i := range f.repos {
		var ps promoterv1alpha1.PromotionStrategy
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: loadNamespace, Name: f.strategyName(i)}, &ps); err != nil {
			continue
		}
		for e, env := range ps.Status.Environments {
			if e < f.opts.argoCDStuck || env.Active.Hydrated.Sha == "" {
				continue
			}
			var app argocd.Application
			key := types.NamespacedName{Namespace: loadNamespace, Name: fmt.Sprintf("%s-%d", f.strategyName(i), e)}
			if err := k8sClient.Get(ctx, key, &app); err != nil {
				continue
			}
			if app.Status.Sync.Revision == env.Active.Hydrated.Sha && app.Status.Health.Status == argocd.HealthStatusHealthy {
				continue
			}
			app.Status.Sync = argocd.SyncStatus{Status: argocd.SyncStatusCodeSynced, Revision: env.Active.Hydrated.Sha}
			app.Status.Health = argocd.HealthStatus{Status: argocd.HealthStatusHealthy}
			// A conflict means the next round has to look again.
			_ = k8sClient.Update(ctx, &app)
		}
	}
}

// setApplicationHealth sets the health of the Application of the e-th environment of every strategy, as Argo CD
// does while a rollout progresses.
func (f *loadFixture) setApplicationHealth(ctx context.Context, e int, health argocd.HealthStatusCode) {
	GinkgoHelper()
	for i := range f.repos {
		key := types.NamespacedName{Namespace: loadNamespace, Name: fmt.Sprintf("%s-%d", f.strategyName(i), e)}
		Eventually(func(g Gomega) {
			var app argocd.Application
			g.Expect(k8sClient.Get(ctx, key, &app)).To(Succeed())
			app.Status.Health = argocd.HealthStatus{Status: health}
			g.Expect(k8sClient.Update(ctx, &app)).To(Succeed())
		}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
	}
}

// deleteGates deletes the gate resources of every strategy.
func (f *loadFixture) deleteGates(ctx context.Context) {
	for i := range f.repos {
		for _, obj := range f.gateObjects(i) {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
	}
}
