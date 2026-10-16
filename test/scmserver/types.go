package scmserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Provider identifies an SCM provider impersonated by the server.
type Provider string

const (
	// GitHub impersonates GitHub Enterprise Server (REST API under /api/v3).
	GitHub Provider = "github"
	// GitLab impersonates GitLab (REST API under /api/v4).
	GitLab Provider = "gitlab"
	// AzureDevOps impersonates Azure DevOps Services (REST API under /{organization}).
	AzureDevOps Provider = "azuredevops"
	// BitbucketCloud impersonates Bitbucket Cloud (REST API under /2.0).
	BitbucketCloud Provider = "bitbucket"
	// Gitea impersonates Gitea (REST API under /api/v1).
	Gitea Provider = "gitea"
	// Forgejo impersonates Forgejo (REST API under /api/v1).
	Forgejo Provider = "forgejo"
)

// AllProviders lists every provider the server impersonates, in a stable order.
var AllProviders = []Provider{GitHub, GitLab, AzureDevOps, BitbucketCloud, Gitea, Forgejo}

// Valid reports whether p is a provider the server impersonates.
func (p Provider) Valid() bool {
	for _, known := range AllProviders {
		if p == known {
			return true
		}
	}
	return false
}

// Duration is a time.Duration that marshals to and from JSON as a Go duration string such as "1.5s".
type Duration struct {
	time.Duration
}

// D returns a Duration wrapping d.
func D(d time.Duration) Duration {
	return Duration{Duration: d}
}

// MarshalJSON encodes the duration as a Go duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(d.String())
	if err != nil {
		return nil, fmt.Errorf("marshal duration: %w", err)
	}
	return b, nil
}

// UnmarshalJSON accepts a Go duration string or a number of nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", s, err)
		}
		d.Duration = parsed
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("duration must be a string such as \"1s\" or a number of nanoseconds")
	}
	d.Duration = time.Duration(n)
	return nil
}

// dirName is the directory holding the provider's repositories under the data directory. It is a constant per
// provider rather than the (already validated) provider value itself, so no request data reaches the path.
func (p Provider) dirName() string {
	switch p {
	case GitHub:
		return "github"
	case GitLab:
		return "gitlab"
	case AzureDevOps:
		return "azuredevops"
	case BitbucketCloud:
		return "bitbucket"
	case Gitea:
		return "gitea"
	case Forgejo:
		return "forgejo"
	default:
		return "unknown"
	}
}

// String returns the provider name.
func (p Provider) String() string {
	return string(p)
}
