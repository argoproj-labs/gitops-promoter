package utils

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	commitStatusGateLabelPrefix = "promoter.argoproj.io/"
	commitStatusGateLabelSuffix = "-commit-status"
	commitStatusGateKindSuffix  = "CommitStatus"
)

// CommitStatusGateLabelKeyForParent returns the parent-gate label key for a CommitStatus gate
// (for example TimedCommitStatus → promoter.argoproj.io/timed-commit-status).
// parent must have TypeMeta.Kind set (as objects returned from the API server do).
func CommitStatusGateLabelKeyForParent(parent client.Object) string {
	kind := parent.GetObjectKind().GroupVersionKind().Kind
	if kind == "" {
		panic("CommitStatusGateLabelKeyForParent: parent has empty TypeMeta.Kind - make sure the TypeMeta field is set")
	}
	stem := strings.TrimSuffix(kind, commitStatusGateKindSuffix)
	return commitStatusGateLabelPrefix + pascalCaseToKebab(stem) + commitStatusGateLabelSuffix
}

// CommitStatusStandardLabels returns the three labels gate controllers set on each CommitStatus:
// parent gate, environment branch, and commit-status key (spec.key).
// parent must have TypeMeta.Kind set (as objects returned from the API server do).
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

// pascalCaseToKebab converts a PascalCase string to kebab-case for gate label keys (for example ArgoCD → argo-cd).
// That is intentional and separate from CommitStatus resource name partial kinds (ArgoCDCommitStatus → argocd)
// and from CommitStatusLabel values such as argocd-health.
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
