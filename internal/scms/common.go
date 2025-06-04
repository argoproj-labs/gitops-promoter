package scms

type ScmProviderType string

const (
	Fake    ScmProviderType = "fake"
	GitHub  ScmProviderType = "github"
	GitLab  ScmProviderType = "gitlab"
	Forgejo ScmProviderType = "forgejo"
)
