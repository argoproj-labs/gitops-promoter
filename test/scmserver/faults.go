package scmserver

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"slices"
	"sync"
)

// NoFaultHeader exempts a request from every fault rule. Test fixtures send it so that faults only hit the system under
// test, e.g. with `git -c http.extraHeader="X-Scmserver-No-Fault: 1"`.
const NoFaultHeader = "X-Scmserver-No-Fault"

// FaultRule injects a failure or delay into matching requests. Rules are evaluated in creation order and the first
// match applies.
type FaultRule struct {
	pathRe *regexp.Regexp

	// Headers are added to the injected response.
	Headers map[string]string `json:"headers,omitempty"`
	// Provider restricts the rule to one provider; empty matches all.
	Provider Provider `json:"provider,omitempty"`
	// Method restricts the rule to one HTTP method; empty matches all.
	Method string `json:"method,omitempty"`
	// Path is a regular expression matched against the request path; empty matches all.
	Path string `json:"path,omitempty"`
	// Body is the raw response body. When empty, a provider-native error body is generated from Message.
	Body string `json:"body,omitempty"`
	// Message is used in the generated provider-native error body. Defaults to the HTTP status text.
	Message string `json:"message,omitempty"`
	// Delay is applied before the fault (or, with Status 0 and Drop false, before normal handling).
	Delay Duration `json:"delay"`
	// ID is assigned by the server.
	ID int64 `json:"id"`
	// Status is the response status code. Zero only applies Delay and lets the request through.
	Status int `json:"status,omitempty"`
	// Times is the number of matching requests the rule applies to before it expires; zero means unlimited.
	Times int `json:"times,omitempty"`
	// Hits counts the requests the rule applied to.
	Hits int `json:"hits"`
	// Probability in (0, 1] applies the rule to a random fraction of matching requests; zero means always.
	Probability float64 `json:"probability,omitempty"`
	// Drop closes the connection without writing a response.
	Drop bool `json:"drop,omitempty"`
}

type faultSet struct {
	rules  []*FaultRule
	mu     sync.Mutex
	nextID int64
}

func (f *faultSet) add(rule FaultRule) (FaultRule, error) {
	if rule.Path != "" {
		re, err := regexp.Compile(rule.Path)
		if err != nil {
			return FaultRule{}, fmt.Errorf("invalid path regexp: %w", err)
		}
		rule.pathRe = re
	}
	if rule.Provider != "" && !rule.Provider.Valid() {
		return FaultRule{}, fmt.Errorf("unknown provider %q", rule.Provider)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	rule.ID = f.nextID
	rule.Hits = 0
	f.rules = append(f.rules, &rule)
	return rule, nil
}

func (f *faultSet) remove(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	before := len(f.rules)
	f.rules = slices.DeleteFunc(f.rules, func(r *FaultRule) bool { return r.ID == id })
	return len(f.rules) != before
}

func (f *faultSet) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = nil
}

func (f *faultSet) list() []FaultRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FaultRule, 0, len(f.rules))
	for _, r := range f.rules {
		out = append(out, *r)
	}
	return out
}

// match returns a copy of the first rule matching the request and records the hit, or nil. Requests carrying
// NoFaultHeader never match.
func (f *faultSet) match(p Provider, r *http.Request) *FaultRule {
	if r.Header.Get(NoFaultHeader) != "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, rule := range f.rules {
		if rule.Provider != "" && rule.Provider != p {
			continue
		}
		if rule.Method != "" && rule.Method != r.Method {
			continue
		}
		if rule.pathRe != nil && !rule.pathRe.MatchString(r.URL.Path) {
			continue
		}
		if rule.Probability > 0 && rand.Float64() >= rule.Probability { //nolint:gosec // fault injection, not security
			continue
		}
		rule.Hits++
		matched := *rule
		if rule.Times > 0 && rule.Hits >= rule.Times {
			f.rules = slices.Delete(f.rules, i, i+1)
		}
		return &matched
	}
	return nil
}
