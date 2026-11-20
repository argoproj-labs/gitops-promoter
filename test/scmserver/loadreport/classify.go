package loadreport

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

// Bucket groups requests by who sent them and which budget they spend.
type Bucket string

const (
	// BucketRESTRead is a GET the promoter's GitHub client made with an installation token.
	BucketRESTRead Bucket = "rest.installation.read"
	// BucketRESTWrite is a POST, PATCH, PUT or DELETE the promoter's GitHub client made with an installation token.
	BucketRESTWrite Bucket = "rest.installation.write"
	// BucketWebRequest is a REST request made with an installation token by anything but the promoter's GitHub
	// client: a WebRequestCommitStatus using the ScmProvider's credentials. It spends the same installation budget.
	BucketWebRequest Bucket = "rest.webrequest"
	// BucketApp is a REST request authenticated as the GitHub App itself (a JWT): installation lookups and
	// installation token minting. It counts against the App's budget, not the installation's.
	BucketApp Bucket = "rest.app"
	// BucketGitRead is an authenticated git smart HTTP request of a fetch or ls-remote.
	BucketGitRead Bucket = "git.read"
	// BucketGitWrite is an authenticated git smart HTTP request of a push.
	BucketGitWrite Bucket = "git.write"
	// BucketGitAuthChallenge is the unauthenticated request git sends first, which the server answers with 401.
	BucketGitAuthChallenge Bucket = "git.auth-challenge"
	// BucketFixture is a request of the test fixture, not of the promoter.
	BucketFixture Bucket = "fixture"
	// BucketOther is anything else, such as a REST request with missing or expired credentials.
	BucketOther Bucket = "other"
)

// githubClientUserAgent is how the promoter's GitHub client (go-github) identifies itself.
const githubClientUserAgent = "go-github"

// Classify returns the bucket of a request.
func Classify(rec scmserver.RequestRecord) Bucket {
	switch {
	case rec.NoFault, rec.Credential == "token", strings.HasPrefix(rec.Credential, "user:"):
		// The promoter authenticates to GitHub as an App only, so a static credential belongs to the fixture.
		return BucketFixture
	case rec.Git && rec.Credential == "":
		if rec.StatusCode == http.StatusUnauthorized {
			return BucketGitAuthChallenge
		}
		return BucketOther
	case rec.Git && strings.Contains(rec.Route, "git-receive-pack"):
		return BucketGitWrite
	case rec.Git:
		return BucketGitRead
	case strings.HasPrefix(rec.Credential, "app:"):
		return BucketApp
	case strings.HasPrefix(rec.Credential, "installation:"):
		if !strings.Contains(rec.UserAgent, githubClientUserAgent) {
			return BucketWebRequest
		}
		if rec.Method == http.MethodGet || rec.Method == http.MethodHead {
			return BucketRESTRead
		}
		return BucketRESTWrite
	default:
		return BucketOther
	}
}

// Selector picks the requests of one fixture out of a request log shared with other fixtures. A fixture owns a
// repository owner (a GitHub organization) and a GitHub App installed on it.
type Selector struct {
	// Owner is the repository owner. It must not be a substring of another fixture's owner.
	Owner string
	// AppID is the GitHub App whose JWT-authenticated requests belong to the fixture.
	AppID int64
	// InstallationID is the installation whose token-authenticated requests belong to the fixture.
	InstallationID int64
}

// Select returns the requests that belong to the fixture: those whose credential is the fixture's App or
// installation, and those addressed to its owner (which is how the unauthenticated requests are attributed).
func Select(recs []scmserver.RequestRecord, sel Selector) []scmserver.RequestRecord {
	app := "app:" + strconv.FormatInt(sel.AppID, 10)
	installation := "installation:" + strconv.FormatInt(sel.InstallationID, 10)
	ownerSegment := "/" + sel.Owner + "/"
	var out []scmserver.RequestRecord
	for _, rec := range recs {
		switch {
		case sel.AppID != 0 && rec.Credential == app,
			sel.InstallationID != 0 && rec.Credential == installation,
			sel.Owner != "" && strings.Contains(rec.Path+"/", ownerSegment):
			out = append(out, rec)
		default:
		}
	}
	return out
}
