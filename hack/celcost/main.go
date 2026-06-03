// Command celcost estimates the static CEL validation cost of the project's CRDs,
// reproducing the kube-apiserver's budget check (k8s.io/apiextensions-apiserver),
// and prints a Markdown report: one table per resource+version with a row per CEL
// rule (and messageExpression) plus a per-schema total row, flagging anything that
// exceeds the per-rule or per-schema limits.
//
// The estimate matches what apiextensions-apiserver's ValidateCustomResourceDefinition
// computes:
//   - per-rule cost  = getExpressionCost(cr, ctx) = MaxCost * cardinality (overflow-guarded)
//   - messageExpr    = cr.MessageExpressionMaxCost (NOT multiplied by cardinality)
//   - per-schema sum = saturating sum of all of the above, compared per OpenAPIv3 schema
//     (i.e. per CRD version), which is how StaticEstimatedCRDCostLimit is applied.
//
// Note that a given kube-apiserver BINARY may be more lenient than the pinned library
// (e.g. on UPDATE it allows already-stored expressions); this reports the library's
// stricter, create-time, version-portable estimate.
//
// The Markdown output is committed as a docs data file and embedded into the
// "Writing CEL Validation Rules" contributor page; regenerate it with
// `make cel-cost-report`.
//
// Usage: go run ./hack/celcost [-o report.md] [config/crd/bases ...]
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	val "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	schemacel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/environment"
	"sigs.k8s.io/yaml"
)

const (
	perRuleLimit   = val.StaticEstimatedCostLimit    // 10,000,000  (per expression)
	perSchemaLimit = val.StaticEstimatedCRDCostLimit // 100,000,000 (per OpenAPIv3 schema / version)
)

// envSet mirrors the env set ValidateCustomResourceDefinition builds by default.
var envSet = environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion())

// ruleCost is the estimated cost of a single CEL expression (a rule or its
// messageExpression) at a given schema path.
type ruleCost struct {
	path string
	expr string
	cost uint64
}

// mul reproduces apiextensions multiplyWithOverflowGuard.
func mul(base, card uint64) uint64 {
	if base == 0 {
		return 0
	}
	if math.MaxUint64/base < card {
		return math.MaxUint64
	}
	return base * card
}

// add is a saturating add, matching TotalCost.ObserveExpressionCost accumulation.
func add(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

// ruleExprCost mirrors apiextensions validation.getExpressionCost: MaxCost * cardinality.
func ruleExprCost(cr schemacel.CompilationResult, c *val.CELSchemaContext) uint64 {
	if c.MaxCardinality != nil {
		return mul(cr.MaxCost, *c.MaxCardinality)
	}
	return mul(cr.MaxCost, cr.MaxCardinality)
}

// walk recurses the schema accumulating per-expression costs, mirroring how
// ValidateCustomResourceDefinitionOpenAPISchema descends and observes costs. Any
// per-node compile failures are appended to warns so they are not silently dropped.
func walk(s *apiext.JSONSchemaProps, c *val.CELSchemaContext, path string, out *[]ruleCost, warns *[]string) {
	if s == nil || c == nil {
		return
	}
	if len(s.XValidations) > 0 {
		ti, err := c.TypeInfo()
		switch {
		case err != nil:
			*warns = append(*warns, fmt.Sprintf("%s: type info error: %v", path, err))
		case ti == nil:
			*warns = append(*warns, fmt.Sprintf("%s: nil type info", path))
		default:
			res, err := schemacel.Compile(ti.Schema, ti.DeclType, celconfig.PerCallLimit, envSet, schemacel.NewExpressionsEnvLoader())
			if err != nil {
				*warns = append(*warns, fmt.Sprintf("%s: compile error: %v", path, err))
			} else {
				for i, cr := range res {
					if cr.Error != nil {
						*warns = append(*warns, fmt.Sprintf("%s[%d]: %s", path, i, cr.Error.Detail))
					}
					*out = append(*out, ruleCost{
						path: path,
						expr: s.XValidations[i].Rule,
						cost: ruleExprCost(cr, c),
					})
					// messageExpression cost is counted raw (not multiplied by cardinality).
					if cr.MessageExpression != nil {
						*out = append(*out, ruleCost{
							path: path,
							expr: s.XValidations[i].MessageExpression,
							cost: cr.MessageExpressionMaxCost,
						})
					}
				}
			}
		}
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := s.Properties[n]
		walk(&p, c.ChildPropertyContext(&p, n), path+"."+n, out, warns)
	}
	if s.Items != nil && s.Items.Schema != nil {
		walk(s.Items.Schema, c.ChildItemsContext(s.Items.Schema), path+"[]", out, warns)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		walk(s.AdditionalProperties.Schema, c.ChildAdditionalPropertiesContext(s.AdditionalProperties.Schema), path+"{}", out, warns)
	}
}

// versionReport holds the cost breakdown for a single CRD version/schema.
type versionReport struct {
	version string
	rules   []ruleCost
	total   uint64
	warns   []string
}

// resourceReport holds all version reports for one CRD file.
type resourceReport struct {
	kind     string
	file     string
	versions []versionReport
}

func analyze(s *apiext.JSONSchemaProps, version string) versionReport {
	vr := versionReport{version: version}
	if s == nil {
		return vr
	}
	walk(s, val.RootCELContext(s), "", &vr.rules, &vr.warns)
	for _, rc := range vr.rules {
		vr.total = add(vr.total, rc.cost)
	}
	// Sort most-expensive first for readability; stable on path for ties.
	sort.SliceStable(vr.rules, func(i, j int) bool {
		if vr.rules[i].cost != vr.rules[j].cost {
			return vr.rules[i].cost > vr.rules[j].cost
		}
		return vr.rules[i].path < vr.rules[j].path
	})
	return vr
}

func main() {
	out := flag.String("o", "", "write the Markdown report to this file instead of stdout")
	flag.Parse()

	dirs := flag.Args()
	if len(dirs) == 0 {
		dirs = []string{"config/crd/bases"}
	}
	var files []string
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(d, "*.yaml"))
		files = append(files, matches...)
	}
	sort.Strings(files)

	var reports []resourceReport
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: read error: %v\n", f, err)
			continue
		}
		var v1crd apiextv1.CustomResourceDefinition
		if err := yaml.Unmarshal(b, &v1crd); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: unmarshal error: %v\n", f, err)
			continue
		}
		if v1crd.Kind != "CustomResourceDefinition" {
			continue
		}
		var crd apiext.CustomResourceDefinition
		if err := apiextv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&v1crd, &crd, nil); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: convert error: %v\n", f, err)
			continue
		}

		schemas := map[string]*apiext.JSONSchemaProps{}
		if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
			name := crd.Spec.Version
			if name == "" {
				name = "(global)"
			}
			schemas[name] = crd.Spec.Validation.OpenAPIV3Schema
		}
		for i := range crd.Spec.Versions {
			v := &crd.Spec.Versions[i]
			if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
				schemas[v.Name] = v.Schema.OpenAPIV3Schema
			}
		}
		vnames := make([]string, 0, len(schemas))
		for n := range schemas {
			vnames = append(vnames, n)
		}
		sort.Strings(vnames)

		rep := resourceReport{kind: crd.Spec.Names.Kind, file: filepath.Base(f)}
		for _, vn := range vnames {
			rep.versions = append(rep.versions, analyze(schemas[vn], vn))
		}
		reports = append(reports, rep)
	}

	md := render(reports)
	if *out == "" {
		fmt.Print(md)
		return
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
}

func render(reports []resourceReport) string {
	var b strings.Builder

	// This output is committed as a docs data file and included via markdown_include;
	// it is intentionally a heading-fragment (no H1) so it nests under a host page.
	b.WriteString("<!-- Generated by `make cel-cost-report` (hack/celcost). DO NOT EDIT. -->\n\n")
	fmt.Fprintf(&b, "Estimated static CEL costs versus kube-apiserver limits, computed from "+
		"`k8s.io/apiextensions-apiserver` (`ValidateCustomResourceDefinition`).\n\n")
	fmt.Fprintf(&b, "- Per-rule limit: `%s`\n", commas(perRuleLimit))
	fmt.Fprintf(&b, "- Per-schema (per CRD version) limit: `%s`\n\n", commas(perSchemaLimit))

	// Summary table.
	b.WriteString("### Summary\n\n")
	b.WriteString("| Resource | Version | Total cost | % of schema limit |\n")
	b.WriteString("|---|---|---:|---:|\n")
	for _, r := range reports {
		for _, v := range r.versions {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				r.kind, v.version, commas(v.total), pct(v.total, perSchemaLimit))
		}
	}
	b.WriteString("\n")

	// Per-resource detail.
	b.WriteString("### Per-resource detail\n\n")
	for _, r := range reports {
		fmt.Fprintf(&b, "#### %s\n\n", r.kind)
		fmt.Fprintf(&b, "Source: `%s`\n\n", r.file)
		for _, v := range r.versions {
			fmt.Fprintf(&b, "##### Version `%s`\n\n", v.version)
			if len(v.rules) == 0 {
				b.WriteString("_No CEL validation rules._\n\n")
			} else {
				b.WriteString("| Path | Cost | % of rule limit | Expression |\n")
				b.WriteString("|---|---:|---:|---|\n")
				for _, rc := range v.rules {
					path := rc.path
					if path == "" {
						path = "(root)"
					}
					fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
						path, commas(rc.cost), pct(rc.cost, perRuleLimit), code(rc.expr))
				}
				fmt.Fprintf(&b, "| **Total** | **%s** | **%s** | |\n\n",
					commas(v.total), pct(v.total, perSchemaLimit))
			}
			if len(v.warns) > 0 {
				b.WriteString("Warnings:\n\n")
				for _, w := range v.warns {
					fmt.Fprintf(&b, "- %s\n", w)
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func pct(v, limit uint64) string {
	if limit == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f%%", float64(v)/float64(limit)*100)
}

// code renders a CEL expression as inline code on a single line for a table cell.
func code(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return "`" + s + "`"
}

func commas(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	pre := len(s) % 3
	if pre > 0 {
		out = append(out, s[:pre]...)
	}
	for i := pre; i < len(s); i += 3 {
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}
