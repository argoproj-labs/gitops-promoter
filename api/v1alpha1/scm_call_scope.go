package v1alpha1

// +kubebuilder:object:root=false
// +kubebuilder:object:generate:false
// +k8s:deepcopy-gen:interfaces=nil
// +k8s:deepcopy-gen=nil

// SCMCallScope supplies scm_calls_* metric and log labels for an SCM API call.
type SCMCallScope interface {
	SCMCallGitRepository() string
	SCMCallGitRepositoryNamespace() string
	SCMCallSCMProvider() string
	SCMCallSCMProviderNamespace() string
	SCMCallSCMProviderKind() string
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

// SCMCallSCMProviderNamespace implements SCMCallScope. A namespaced provider is in the same
// namespace as the GitRepository. ClusterScmProvider is cluster-scoped, so the namespace is empty.
func (r *GitRepository) SCMCallSCMProviderNamespace() string {
	if scmProviderRefKind(r.Spec.ScmProviderRef) == ClusterScmProviderKind {
		return ""
	}
	return r.Namespace
}

// SCMCallSCMProviderKind implements SCMCallScope.
func (r *GitRepository) SCMCallSCMProviderKind() string {
	return scmProviderRefKind(r.Spec.ScmProviderRef)
}

// SCMCallGitRepository implements SCMCallScope.
func (s *ScmProvider) SCMCallGitRepository() string { return "" }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (s *ScmProvider) SCMCallGitRepositoryNamespace() string { return "" }

// SCMCallSCMProvider implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProvider() string { return s.Name }

// SCMCallSCMProviderNamespace implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProviderNamespace() string { return s.Namespace }

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProviderKind() string { return ScmProviderKind }

// SCMCallGitRepository implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepository() string { return "" }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepositoryNamespace() string { return "" }

// SCMCallSCMProvider implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProvider() string { return s.Name }

// SCMCallSCMProviderNamespace implements SCMCallScope. ClusterScmProvider is cluster-scoped.
func (s *ClusterScmProvider) SCMCallSCMProviderNamespace() string { return "" }

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProviderKind() string { return ClusterScmProviderKind }

var (
	_ SCMCallScope = &GitRepository{}
	_ SCMCallScope = &ScmProvider{}
	_ SCMCallScope = &ClusterScmProvider{}
)
