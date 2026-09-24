package git

// Test-only aliases for unexported helpers. Visible to package git_test in this directory,
// not to other packages that import git.
var (
	GitChildEnv          = gitChildEnv
	GitBin               = gitBin
	ParseCommitLogOutput = parseCommitLogOutput
	ParseCatFileBatch    = parseCatFileBatch
	FullObjectID         = fullObjectID
)

// Trailers exposes the parsed trailers of a commitObject to package git_test.
func (c commitObject) Trailers() map[string][]string { return c.trailers }
