package loadreport

import (
	"time"

	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

// Kind is how a scenario's requests are turned into numbers.
type Kind string

const (
	// KindSteady is a state that persists: requests are reported per hour at the shipped intervals, and a write
	// during the observation marks the scenario unstable.
	KindSteady Kind = "steady"
	// KindEvent is something that happens once: requests are reported as totals for the event.
	KindEvent Kind = "event"
	// KindWindow is an observation at real speed of a state that is not driven by the compressed timers, such as
	// a throttled API: requests are reported as totals and per minute.
	KindWindow Kind = "window"
)

// Topology describes what a scenario's fixture holds.
type Topology struct {
	// WebRequest describes the WebRequestCommitStatus of the fixture, if it has one.
	WebRequest string `json:"webRequest,omitempty"`
	// Gates lists the gates configured besides the ordering gate every PromotionStrategy has.
	Gates []string `json:"gates,omitempty"`
	// PromotionStrategies is the number of PromotionStrategies, each with its own repository.
	PromotionStrategies int `json:"promotionStrategies"`
	// Environments is the number of environments per PromotionStrategy.
	Environments int `json:"environments"`
	// OpenPullRequests is the number of open pull requests during the scenario, across all strategies.
	OpenPullRequests int `json:"openPullRequests"`
}

// Scenario is what the harness knows about a measurement.
type Scenario struct {
	// Start and End bound the measurement.
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Name identifies the scenario, such as "steady/held/manual".
	Name string `json:"name"`
	// Group is the report section the scenario belongs to.
	Group string `json:"group"`
	Kind  Kind   `json:"kind"`
	// Description says what the scenario measures.
	Description string `json:"description"`
	// Notes are remarks of the harness, such as why an event did not settle.
	Notes    []string `json:"notes,omitempty"`
	Topology Topology `json:"topology"`
	// Compression is how much faster than shipped the periodic timers ran. 1 for events and windows.
	Compression float64 `json:"compression"`
	// Requeue is the controllers' requeue interval during the measurement.
	Requeue scmserver.Duration `json:"requeue"`
	// Poll is the WebRequestCommitStatus polling interval during the measurement, when the fixture has one.
	Poll scmserver.Duration `json:"poll,omitzero"`
	// Truncated is true when the server's request log dropped requests during the measurement.
	Truncated bool `json:"truncated,omitempty"`
	// Settled is false for an event whose end condition was not reached before the timeout.
	Settled bool `json:"settled"`
}

// Budgets are the provider limits the report compares against. They only scale the "share of budget" figures.
type Budgets struct {
	// InstallationPerHour is the primary rate limit of a GitHub App installation.
	InstallationPerHour int `json:"installationPerHour"`
	// ContentCreatesPerMinute and ContentCreatesPerHour are the secondary limits on requests that create content.
	ContentCreatesPerMinute int `json:"contentCreatesPerMinute"`
	ContentCreatesPerHour   int `json:"contentCreatesPerHour"`
	// PointsPerMinute is the secondary limit on REST points, where a read costs ReadPoints and a write WritePoints.
	PointsPerMinute int `json:"pointsPerMinute"`
	ReadPoints      int `json:"readPoints"`
	WritePoints     int `json:"writePoints"`
}

// DefaultBudgets returns the limits GitHub documents for github.com
// (https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api): 5000 requests an hour for an
// installation, which is the minimum (it grows with the organization's repositories and users up to 12500, and is
// 15000 on GitHub Enterprise Cloud), and the secondary limits of 80 content-generating requests a minute, 500 an
// hour, and 900 points a minute with most reads at 1 point and most writes at 5.
func DefaultBudgets() Budgets {
	return Budgets{
		InstallationPerHour:     5000,
		ContentCreatesPerMinute: 80,
		ContentCreatesPerHour:   500,
		PointsPerMinute:         900,
		ReadPoints:              1,
		WritePoints:             5,
	}
}
