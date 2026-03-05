package constants

// Commit-ref values used by GitCommitStatus.Spec.Target and WebRequestCommitStatus.Spec.ReportOn
// to indicate which commit (active/deployed vs proposed) to validate or report on.
const (
	// CommitRefActive is the value for "active" (currently deployed) commit.
	CommitRefActive = "active"
	// CommitRefProposed is the value for "proposed" (to be promoted) commit.
	CommitRefProposed = "proposed"
)
