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

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ScmProvider) SCMCallSCMProviderKind() string { return ScmProviderKind }

// SCMCallGitRepository implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepository() string { return "" }

// SCMCallGitRepositoryNamespace implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallGitRepositoryNamespace() string { return "" }

// SCMCallSCMProvider implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProvider() string { return s.Name }

// SCMCallSCMProviderKind implements SCMCallScope.
func (s *ClusterScmProvider) SCMCallSCMProviderKind() string { return ClusterScmProviderKind }

var (
	_ SCMCallScope = &GitRepository{}
	_ SCMCallScope = &ScmProvider{}
	_ SCMCallScope = &ClusterScmProvider{}
)
