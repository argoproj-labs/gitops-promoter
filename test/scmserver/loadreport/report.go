package loadreport

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the version of the JSON report format.
const SchemaVersion = 1

// Check is a consistency check the harness ran on its own measurements.
type Check struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Pass   bool   `json:"pass"`
}

// Report is the outcome of a harness run.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	// Provider is the SCM provider measured.
	Provider string `json:"provider"`
	// Revision identifies the promoter source that was measured, when known.
	Revision string `json:"revision,omitempty"`
	// Limitations are what the numbers do not cover.
	Limitations []string `json:"limitations,omitempty"`
	Scenarios   []Result `json:"scenarios"`
	Checks      []Check  `json:"checks,omitempty"`
	Budgets     Budgets  `json:"budgets"`
	// ShippedRequeue and ShippedPoll are the intervals steady scenarios are extrapolated to.
	ShippedRequeue string `json:"shippedRequeue"`
	ShippedPoll    string `json:"shippedPoll"`
	SchemaVersion  int    `json:"schemaVersion"`
}

// Agreement checks that the values agree within a relative tolerance, such as the same rate measured at two
// compression factors, or a per-strategy rate measured at several scales.
func Agreement(name string, tolerance float64, labels []string, values []float64) Check {
	lo, hi := math.Inf(1), math.Inf(-1)
	parts := make([]string, 0, len(values))
	for i, v := range values {
		lo, hi = min(lo, v), max(hi, v)
		parts = append(parts, labels[i]+"="+formatNumber(v))
	}
	spread := 0.0
	if hi > 0 {
		spread = (hi - lo) / hi
	}
	return Check{
		Name:   name,
		Pass:   len(values) > 0 && spread <= tolerance,
		Detail: fmt.Sprintf("%s (spread %.1f%%, tolerance %.0f%%)", strings.Join(parts, ", "), spread*100, tolerance*100),
	}
}

// WriteJSON writes the report as indented JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}

// WriteMarkdown writes the report for people: a table per group of scenarios, then the routes of each scenario.
func (r *Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	b.WriteString("# SCM API load report\n\n")
	fmt.Fprintf(&b, "Provider: %s. Generated %s.", r.Provider, r.GeneratedAt.UTC().Format(time.RFC3339))
	if r.Revision != "" {
		fmt.Fprintf(&b, " Revision %s.", r.Revision)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Steady-state rates are extrapolated to the shipped %s requeue interval and a %s "+
		"WebRequestCommitStatus polling interval. Budget share is of %d REST requests an hour per installation.\n\n",
		r.ShippedRequeue, r.ShippedPoll, r.Budgets.InstallationPerHour)

	for _, group := range r.groups() {
		results := r.inGroup(group)
		fmt.Fprintf(&b, "## %s\n\n", group)
		switch results[0].Kind {
		case KindSteady:
			writeSteadyTable(&b, results)
		case KindWindow:
			writeWindowTable(&b, results)
		default:
			writeEventTable(&b, results)
		}
	}

	if len(r.Checks) > 0 {
		b.WriteString("## Consistency checks\n\n| Check | Result | Detail |\n|---|---|---|\n")
		for _, c := range r.Checks {
			outcome := "pass"
			if !c.Pass {
				outcome = "**FAIL**"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", c.Name, outcome, c.Detail)
		}
		b.WriteString("\n")
	}
	if len(r.Limitations) > 0 {
		b.WriteString("## Limitations\n\n")
		for _, l := range r.Limitations {
			fmt.Fprintf(&b, "- %s\n", l)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Requests by route\n\n")
	for _, res := range r.Scenarios {
		writeRoutes(&b, res)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// groups returns the scenario groups in order of first appearance.
func (r *Report) groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, res := range r.Scenarios {
		if !seen[res.Group] {
			seen[res.Group] = true
			out = append(out, res.Group)
		}
	}
	return out
}

func (r *Report) inGroup(group string) []Result {
	var out []Result
	for _, res := range r.Scenarios {
		if res.Group == group {
			out = append(out, res)
		}
	}
	return out
}

func writeSteadyTable(b *strings.Builder, results []Result) {
	b.WriteString("Per hour, at shipped intervals. \"Git commands\" are fetches, ls-remotes and pushes; each is " +
		"several HTTP requests.\n\n")
	b.WriteString("| Scenario | Strategies x envs | Open PRs | REST/h | of which WRCS | REST writes/h | Budget share | " +
		"REST/h per strategy | Git commands/h | Git HTTP/h | REST per requeue | WRCS per poll | Git commands per requeue | Stable |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, res := range results {
		rates := res.Rates
		stable := "yes"
		if !res.Stable {
			stable = "**no**"
		}
		fmt.Fprintf(b, "| %s | %d x %d | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			res.Name, res.Topology.PromotionStrategies, res.Topology.Environments, res.Topology.OpenPullRequests,
			formatNumber(rates.RESTPerHour), formatNumber(rates.WebRequestPerHour), formatNumber(rates.RESTWritePerHour),
			formatPercent(rates.InstallationBudgetShare), formatNumber(rates.RESTPerHourPerStrategy),
			formatNumber(rates.GitCommandsPerHour), formatNumber(rates.GitHTTPPerHour),
			formatNumber(rates.RESTPerCycle), formatNumber(rates.WebRequestPerPoll), formatNumber(rates.GitCommandsPerCycle),
			stable)
	}
	b.WriteString("\n")
}

func writeEventTable(b *strings.Builder, results []Result) {
	b.WriteString("Totals for the event, from its trigger until the promoter went quiet.\n\n")
	b.WriteString("| Scenario | Strategies x envs | REST | reads | writes | creates | WRCS | App (JWT) | Git commands | " +
		"Fetch rounds | Pushes | Git HTTP | Peak REST/min | Peak creates/min | Seconds | Settled |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, res := range results {
		t := res.Totals
		settled := "yes"
		if !res.Settled {
			settled = "**no**"
		}
		peaks := res.Peaks
		if peaks == nil {
			peaks = &Peaks{}
		}
		fmt.Fprintf(b, "| %s | %d x %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %.0f | %s |\n",
			res.Name, res.Topology.PromotionStrategies, res.Topology.Environments,
			t.REST, t.RESTRead, t.RESTWrite, t.RESTCreate, t.WebRequest, t.RESTApp,
			t.GitReadCommands+t.GitPushes, t.GitFetchRounds, t.GitPushes, t.GitHTTP,
			peaks.RESTPerMinute, peaks.RESTCreatePerMinute, res.Seconds, settled)
	}
	b.WriteString("\n")
}

func writeWindowTable(b *strings.Builder, results []Result) {
	b.WriteString("Observed at real speed, without time compression.\n\n")
	b.WriteString("| Scenario | Seconds | REST | of which WRCS | 4xx | 5xx | REST/h | Peak REST/min | Git commands | Git HTTP |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, res := range results {
		t := res.Totals
		peaks := res.Peaks
		if peaks == nil {
			peaks = &Peaks{}
		}
		fmt.Fprintf(b, "| %s | %.0f | %d | %d | %d | %d | %s | %d | %d | %d |\n",
			res.Name, res.Seconds, t.REST, t.WebRequest, t.Status4xx, t.Status5xx,
			formatNumber(res.Rates.RESTPerHour), peaks.RESTPerMinute, t.GitReadCommands+t.GitPushes, t.GitHTTP)
	}
	b.WriteString("\n")
}

func writeRoutes(b *strings.Builder, res Result) {
	fmt.Fprintf(b, "### %s\n\n%s\n\n", res.Name, res.Description)
	if res.Topology.WebRequest != "" {
		fmt.Fprintf(b, "WebRequestCommitStatus: %s.\n\n", res.Topology.WebRequest)
	}
	if len(res.Topology.Gates) > 0 {
		fmt.Fprintf(b, "Gates: %s.\n\n", strings.Join(res.Topology.Gates, ", "))
	}
	if res.Kind == KindSteady {
		fmt.Fprintf(b, "Observed %.0fs at %sx compression (requeue %s", res.Seconds, formatNumber(res.Compression), res.Requeue)
		if res.Poll.Duration > 0 {
			fmt.Fprintf(b, ", poll %s", res.Poll)
		}
		b.WriteString(").\n\n")
	}
	for _, note := range res.Notes {
		fmt.Fprintf(b, "Note: %s\n\n", note)
	}
	if res.Truncated {
		b.WriteString("**The request log dropped requests during this measurement; the counts are low.**\n\n")
	}
	if len(res.Instability) > 0 {
		fmt.Fprintf(b, "**Not steady:** %s.\n\n", strings.Join(res.Instability, "; "))
	}
	if len(res.Routes) == 0 {
		b.WriteString("No requests.\n\n")
		return
	}
	steady := res.Kind == KindSteady
	if steady {
		b.WriteString("| Bucket | Route | Status | Count | Per interval | Per hour (shipped) |\n|---|---|---|---|---|---|\n")
	} else {
		b.WriteString("| Bucket | Route | Status | Count |\n|---|---|---|---|\n")
	}
	for _, rc := range res.Routes {
		if steady {
			fmt.Fprintf(b, "| %s | `%s` | %d | %d | %s | %s |\n", rc.Bucket, ShortRoute(rc.Route), rc.Status, rc.Count,
				formatNumber(rc.PerCycle), formatNumber(rc.PerHour))
		} else {
			fmt.Fprintf(b, "| %s | `%s` | %d | %d |\n", rc.Bucket, ShortRoute(rc.Route), rc.Status, rc.Count)
		}
	}
	b.WriteString("\n")
}

// formatNumber prints a rate with as many decimals as its size warrants: 1234, 36.2, 0.98, 0.
func formatNumber(v float64) string {
	abs := math.Abs(v)
	switch {
	case abs == 0:
		return "0"
	case abs >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case abs >= 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	default:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
}

func formatPercent(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 2, 64) + "%"
}
