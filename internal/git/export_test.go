package git

// Test-only aliases for unexported helpers. Visible to package git_test in this directory,
// not to other packages that import git.
var (
	GitChildEnv = gitChildEnv
	GitBin      = gitBin
)
