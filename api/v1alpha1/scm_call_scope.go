package v1alpha1

import "strings"

// +kubebuilder:object:root=false
// +kubebuilder:object:generate:false
// +k8s:deepcopy-gen:interfaces=nil
// +k8s:deepcopy-gen=nil

// SCMCallScope supplies scm_calls_* metric and log labels for an SCM API call.
type SCMCallScope interface {
	SCMCallGitRepository() string
	SCMCallGitRepositoryNamespace() string
	SCMCallSCMProvider() string
	SCMCallSCMProviderKind() string
	// SCMCallAccount is the account a rate-limit bucket belongs to when the provider
	// credential is shared across accounts. Empty means the credential is the whole bucket.
	SCMCallAccount() string
}

func scmProviderRefKind(ref ScmProviderObjectReference) string {
	if ref.Kind == "" {
		return ScmProviderKind
	}
	return ref.Kind
}

// SCMCallGitRepository implements SCMCallScope.
func (r *GitRepository) SCMCallGitRepository() string { return r.Name }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (r *GitRepository) SCMCallGitRepositoryNamespace() string { return r.Namespace }

// SCMCallSCMProvider implements SCMCallScope.
func (r *GitRepository) SCMCallSCMProvider() string { return r.Spec.ScmProviderRef.Name }

// SCMCallSCMProviderKind implements SCMCallScope.
func (r *GitRepository) SCMCallSCMProviderKind() string {
	return scmProviderRefKind(r.Spec.ScmProviderRef)
}

// SCMCallAccount implements SCMCallScope. GitHub App installation tokens are limited per
// account, so the repository owner (org or user) is that account. GitHub treats that
// login as case-insensitive, so the label is lowercased and mixed-case specs share one
// series. Other providers limit the credential on the SCM provider, which scm_provider
// already identifies.
func (r *GitRepository) SCMCallAccount() string {
	if r.Spec.GitHub != nil {
		return strings.ToLower(r.Spec.GitHub.Owner)
	}
	return ""
}

// SCMCallGitRepository implements SCMCallScope.
func (s *ScmProvider) SCMCallGitRepository() string { return "" }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (s *ScmProvider) SCMCallGitRepositoryNamespace() string { return "" }

// SCMCallSCMProvider implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProvider() string { return s.Name }

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProviderKind() string { return ScmProviderKind }

// SCMCallAccount implements SCMCallScope. Provider-scoped calls are not tied to an installation account.
func (s *ScmProvider) SCMCallAccount() string { return "" }

// SCMCallGitRepository implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepository() string { return "" }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepositoryNamespace() string { return "" }

// SCMCallSCMProvider implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProvider() string { return s.Name }

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProviderKind() string { return ClusterScmProviderKind }

// SCMCallAccount implements SCMCallScope. Provider-scoped calls are not tied to an installation account.
func (s *ClusterScmProvider) SCMCallAccount() string { return "" }

var (
	_ SCMCallScope = &GitRepository{}
	_ SCMCallScope = &ScmProvider{}
	_ SCMCallScope = &ClusterScmProvider{}
)
