package utils

import (
	"fmt"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const (
	commitStatusGateLabelSuffix = "-commit-status"
	commitStatusGateKindSuffix  = "CommitStatus"
)

// commitStatusGateKind returns the API Kind of a *CommitStatus gate parent.
// Uses TypeMeta.Kind when set; otherwise resolves from GetScheme via apiutil.GVKForObject.
// Panics when Kind cannot be resolved (unregistered type, ambiguous GVK, etc.).
func commitStatusGateKind(parent client.Object) string {
	kind := parent.GetObjectKind().GroupVersionKind().Kind
	if kind != "" {
		return kind
	}
	gvk, err := apiutil.GVKForObject(parent, GetScheme())
	if err != nil {
		panic(fmt.Sprintf("commitStatusGateKind: resolve kind for %T: %v", parent, err))
	}
	return gvk.Kind
}

// commitStatusGateKebabStem returns the kebab-case gate-type stem from a parent Kind
// (for example TimedCommitStatus → timed, ArgoCDCommitStatus → argo-cd, WebRequestCommitStatus → web-request).
func commitStatusGateKebabStem(parent client.Object) string {
	stem := strings.TrimSuffix(commitStatusGateKind(parent), commitStatusGateKindSuffix)
	return pascalCaseToKebab(stem)
}

// CommitStatusGateLabelKeyForParent returns the parent-gate label key for a CommitStatus gate
// (for example TimedCommitStatus → promoter.argoproj.io/timed-commit-status).
// Kind is taken from TypeMeta when set; otherwise resolved from GetScheme via apiutil.GVKForObject.
func CommitStatusGateLabelKeyForParent(parent client.Object) string {
	return promoterv1alpha1.CommitStatusGateLabelPrefix + commitStatusGateKebabStem(parent) + commitStatusGateLabelSuffix
}

// CommitStatusStandardLabels returns the three labels gate controllers set on each CommitStatus:
// parent gate, environment branch, and commit-status key (spec.key).
func CommitStatusStandardLabels(parent client.Object, branch, commitStatusKey string) map[string]string {
	return map[string]string{
		CommitStatusGateLabelKeyForParent(parent): KubeSafeLabel(parent.GetName()),
		promoterv1alpha1.EnvironmentLabel:         KubeSafeLabel(branch),
		promoterv1alpha1.CommitStatusLabel:        commitStatusKey,
	}
}

func pascalCaseNeedsDashBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	prev := rune(s[i-1])
	if prev >= 'a' && prev <= 'z' {
		return true
	}
	if i+1 >= len(s) {
		return false
	}
	return rune(s[i+1]) >= 'a' && rune(s[i+1]) <= 'z'
}

// pascalCaseToKebab converts a PascalCase string to kebab-case (for example ArgoCD → argo-cd).
// Used for both CommitStatus resource name suffixes and parent-gate label keys via commitStatusGateKebabStem.
// Unrelated to CommitStatusLabel values such as argocd-health.
func pascalCaseToKebab(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if pascalCaseNeedsDashBefore(s, i) {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
