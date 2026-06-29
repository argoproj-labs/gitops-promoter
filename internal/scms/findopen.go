package scms

import "time"

// FindOpenResult holds the outcome of locating an open pull request on the SCM.
type FindOpenResult struct {
	ID             string
	CreationTime   time.Time
	SCMLabels      []string
	Found          bool
	LabelsReported bool
}
