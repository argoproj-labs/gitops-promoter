package constants

// Commit-ref values used by GitCommitStatus.Spec.Target and WebRequestCommitStatus.Spec.ReportOn
// to indicate which commit (active/deployed vs proposed) to validate or report on.
const (
	// CommitRefActive is the value for "active" (currently deployed) commit.
	CommitRefActive = "active"
	// CommitRefProposed is the value for "proposed" (to be promoted) commit.
	CommitRefProposed = "proposed"
)

// ValidatedCommitStatusRefShaField is the cache field index over the commits a ValidatedCommitStatus last
// saw at the head of its dry branch and on each environment tag (status.dryBranch.sha and
// status.environments[].tag.sha). The webhook receiver uses it to find the gates that read a pushed ref.
const ValidatedCommitStatusRefShaField = ".status.refShas"
