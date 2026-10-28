package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

// Promoter-specific flags. Upstream behavior is unchanged unless these are set.
// make deadcode passes them.
var (
	dirFlag = flag.String("dir", "", "load packages from this directory instead of the current directory")

	reflectionFlag = flag.Bool("reflection", false, "also report reachable functions that have no call-graph edge")
	allowFlag      = flag.String("allow", "", "file of qualified names to hide; a live or missing name fails the run")
	canaryFlag     = flag.String("canary", "", "qualified name that must be reached by a call; exit 2 if it is not")
	failFlag       = flag.Bool("fail", false, "exit 1 when dead functions are reported")

	entryFlag  stringList
	skipFlag   stringList
	suffixFlag stringList
)

func init() {
	flag.Var(&entryFlag, "entry", "qualified name to treat as a root (repeatable)")
	flag.Var(&skipFlag, "skip", "package-path fragment to drop from roots and the report (repeatable)")
	flag.Var(&suffixFlag, "ignore-suffix", "hide functions whose name ends with this suffix (repeatable)")
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func exitf(code int, format string, args ...any) {
	log.Printf(format, args...)
	os.Exit(code)
}

func skippedPath(path string, skips []string) bool {
	for _, skip := range skips {
		if skip != "" && strings.Contains(path, skip) {
			return true
		}
	}
	return false
}

func ignoredSuffix(name string, suffixes []string) bool {
	base := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		base = name[i+1:]
	}
	for _, suffix := range suffixes {
		if suffix != "" && strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// kindOf reports how a function relates to production code.
// The empty string means a call from a live function reaches it.
func kindOf(reachable, called bool) string {
	switch {
	case !reachable:
		return "unreachable"
	case !called:
		return "reflection-only"
	default:
		return ""
	}
}

func hasInEdge(n *callgraph.Node) bool {
	return n != nil && len(n.In) > 0
}

func entryRoots(funcs []*ssa.Function, entries []string) ([]*ssa.Function, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	found := make(map[string]bool, len(entries))
	for _, entry := range entries {
		found[entry] = false
	}
	var roots []*ssa.Function
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		name := prettyName(fn, true)
		if _, ok := found[name]; !ok {
			continue
		}
		found[name] = true
		roots = append(roots, fn)
	}
	var missing []string
	for _, entry := range entries {
		if !found[entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("entry not in program: %s", strings.Join(missing, ", "))
	}
	return roots, nil
}

type allowEntry struct {
	name string
	line int
}

type allowlist struct {
	byName map[string]allowEntry
	order  []allowEntry
}

func loadAllowlist(path string) (*allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return parseAllowlist(string(data))
}

func parseAllowlist(text string) (*allowlist, error) {
	out := &allowlist{byName: map[string]allowEntry{}}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := out.byName[line]; ok {
			return nil, fmt.Errorf("duplicate allowlist entry %q on line %d", line, i+1)
		}
		entry := allowEntry{name: line, line: i + 1}
		out.byName[line] = entry
		out.order = append(out.order, entry)
	}
	return out, nil
}

func formatDeadFunc(pos jsonPosition, kind, qual string) string {
	return fmt.Sprintf("%s: %s func: %s", pos, kind, qual)
}

func formatAllowProblem(file string, line int, kind, name string) string {
	return fmt.Sprintf("%s:%d:1: %s: %s", file, line, kind, name)
}

func relToCwd(path string) string {
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
