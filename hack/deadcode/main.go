// Command deadcode reports promoter functions that production code cannot reach.
//
// golang.org/x/tools/cmd/deadcode misses three sets when invoked the way this
// repo used to invoke it (deadcode -test, filtered to internal/):
//
//   - functions outside internal/, because the filter drops them
//   - functions whose only callers are tests, because -test makes tests roots
//   - exported methods of types that have been converted to an interface:
//     Rapid Type Analysis marks every such method reachable via reflection,
//     with no call-graph edge
//
// This command loads the operator module once, without tests, and reports both
// unreachable functions and reflection-only functions. Run it from the repo
// root with `make deadcode`.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const modulePath = "github.com/argoproj-labs/gitops-promoter"

// canary is a method production code reaches through an interface. If the
// analysis reports it unused, the result cannot be trusted.
const canary = modulePath + "/internal/controller.ChangeTransferPolicyReconciler.Reconcile"

func main() {
	root := flag.String("root", "", "path to the operator module root")
	allowFile := flag.String("allow", "", "allowlist file (default: <root>/hack/deadcode/allowlist.txt)")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "deadcode: -root is required")
		os.Exit(2)
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deadcode: %v\n", err)
		os.Exit(2)
	}
	allowPath := *allowFile
	if allowPath == "" {
		allowPath = filepath.Join(rootPath, "hack", "deadcode", "allowlist.txt")
	}

	allow, err := loadAllowlist(allowPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deadcode: %v\n", err)
		os.Exit(2)
	}

	findings, err := analyze(rootPath, allowPath, allow)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deadcode: %v\n", err)
		os.Exit(2)
	}
	if len(findings) == 0 {
		fmt.Fprintln(os.Stderr, "deadcode: no unused functions")
		return
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	os.Exit(1)
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

func ignoredByName(name string) bool {
	base := name
	if i := strings.LastIndex(name, "."); i >= 0 {
		base = name[i+1:]
	}
	return strings.HasSuffix(base, "ForTest") || strings.HasSuffix(base, "ForTests")
}

// kind is "" when production code can reach the function.
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

type finding struct {
	file string
	line int
	col  int
	kind string
	name string
}

func (f finding) String() string {
	if f.kind == "unreachable" || f.kind == "reflection-only" {
		return fmt.Sprintf("%s:%d:%d: %s func: %s", f.file, f.line, f.col, f.kind, f.name)
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s", f.file, f.line, f.col, f.kind, f.name)
}

type funcFact struct {
	name      string
	pos       token.Position
	generated bool
	marker    bool
	reachable bool
	called    bool
}

func analyze(root, allowPath string, allow *allowlist) ([]finding, error) {
	fmt.Fprintf(os.Stderr, "deadcode: loading %s\n", root)
	cfg := &packages.Config{
		Dir:   root,
		Mode:  packages.LoadAllSyntax | packages.NeedModule,
		Tests: false,
	}
	initial, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	if len(initial) == 0 {
		return nil, fmt.Errorf("no packages")
	}
	if packages.PrintErrors(initial) > 0 {
		return nil, fmt.Errorf("packages contain errors")
	}

	fmt.Fprintln(os.Stderr, "deadcode: building SSA")
	prog, ssaPkgs := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	mains := ssautil.MainPackages(ssaPkgs)
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main packages")
	}

	var (
		facts     = map[token.Position]*funcFact{}
		generated = map[string]bool{}
	)
	packages.Visit(initial, nil, func(p *packages.Package) {
		if !inModule(p.PkgPath) || skippedPackage(p.PkgPath) {
			return
		}
		var ifaces []*types.Interface
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			typeName, ok := obj.(*types.TypeName)
			if !ok || !types.IsInterface(typeName.Type()) {
				continue
			}
			ifaces = append(ifaces, typeName.Type().Underlying().(*types.Interface))
		}

		for _, file := range p.Syntax {
			filename := p.Fset.File(file.Pos()).Name()
			if ast.IsGenerated(file) {
				generated[filename] = true
			}
			for _, decl := range file.Decls {
				fnDecl, ok := decl.(*ast.FuncDecl)
				if !ok || fnDecl.Body == nil {
					continue
				}
				obj := p.TypesInfo.Defs[fnDecl.Name]
				fnObj, ok := obj.(*types.Func)
				if !ok {
					continue
				}
				fn := prog.FuncValue(fnObj)
				if fn == nil {
					continue
				}
				pos := p.Fset.Position(fn.Pos())
				if !pos.IsValid() {
					continue
				}
				if _, ok := facts[pos]; ok {
					continue
				}
				facts[pos] = &funcFact{
					name:      qualifiedName(fn),
					pos:       pos,
					generated: generated[filename],
					marker:    isMarkerMethod(fnDecl, fn, ifaces),
				}
			}
		}
	})

	prog.Build()
	fset := prog.Fset

	var roots []*ssa.Function
	rootPos := map[token.Position]bool{}
	for _, main := range mains {
		for _, name := range []string{"init", "main"} {
			fn := main.Func(name)
			if fn == nil {
				continue
			}
			roots = append(roots, fn)
			if fn.Pos().IsValid() {
				rootPos[fset.Position(fn.Pos())] = true
			}
		}
	}

	fmt.Fprintln(os.Stderr, "deadcode: analyzing reachability")
	res := rta.Analyze(roots, true)

	reachablePos := map[token.Position]bool{}
	calledPos := map[token.Position]bool{}
	for fn := range res.Reachable {
		if !fn.Pos().IsValid() {
			continue
		}
		pos := fset.Position(fn.Pos())
		reachablePos[pos] = true
		// Any incoming edge counts, including the nil-site edges from
		// reflect.Value.Call. Those edges mark a function value that some
		// live function passed to reflection (expr's urlValuesEncode).
		// Exported methods that RTA keeps only because their receiver type
		// was converted to an interface never get a node or an edge.
		if rootPos[pos] || hasInEdge(res.CallGraph.Nodes[fn]) {
			calledPos[pos] = true
		}
	}
	for pos := range rootPos {
		calledPos[pos] = true
		reachablePos[pos] = true
	}

	relAllow := allowPath
	if rel, err := filepath.Rel(root, allowPath); err == nil && !strings.HasPrefix(rel, "..") {
		relAllow = rel
	}

	matched := map[string]bool{}
	var canaryFact *funcFact
	for pos, fact := range facts {
		fact.reachable = reachablePos[pos]
		fact.called = calledPos[pos]
		if fact.name == canary {
			canaryFact = fact
		}
		if _, ok := allow.byName[fact.name]; ok {
			matched[fact.name] = true
		}
	}
	if canaryFact == nil {
		return nil, fmt.Errorf("canary %s is not in the program", canary)
	}
	if kindOf(canaryFact.reachable, canaryFact.called) != "" {
		return nil, fmt.Errorf("canary %s looks unused; the analysis is wrong", canary)
	}

	var findings []finding
	for _, fact := range facts {
		if fact.generated || fact.marker || !inModule(packagePath(fact.name)) {
			continue
		}
		kind := kindOf(fact.reachable, fact.called)
		if _, allowed := allow.byName[fact.name]; allowed {
			if kind == "" {
				findings = append(findings, allowFinding(relAllow, allow.byName[fact.name], "allowlist entry is reachable from production"))
			}
			continue
		}
		if kind == "" || ignoredByName(fact.name) {
			continue
		}
		rel, err := filepath.Rel(root, fact.pos.Filename)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = fact.pos.Filename
		}
		findings = append(findings, finding{
			file: rel,
			line: fact.pos.Line,
			col:  fact.pos.Column,
			kind: kind,
			name: fact.name,
		})
	}
	for _, entry := range allow.order {
		if matched[entry.name] {
			continue
		}
		findings = append(findings, allowFinding(relAllow, entry, "stale allowlist entry"))
	}

	slices.SortFunc(findings, func(a, b finding) int {
		if c := strings.Compare(a.file, b.file); c != 0 {
			return c
		}
		if a.line != b.line {
			return a.line - b.line
		}
		return strings.Compare(a.name, b.name)
	})
	return findings, nil
}

func allowFinding(file string, entry allowEntry, kind string) finding {
	return finding{file: file, line: entry.line, col: 1, kind: kind, name: entry.name}
}

func packagePath(qualified string) string {
	// qualified is path.Func or path.Type.Method. The package path is everything
	// before the first dot that follows a slash-separated path. Module paths have
	// dots (argoproj-labs has none, but x/tools style paths do). Split on the last
	// element: package path contains slashes, the selector does not.
	slash := strings.LastIndex(qualified, "/")
	rest := qualified
	prefix := ""
	if slash >= 0 {
		prefix = qualified[:slash+1]
		rest = qualified[slash+1:]
	}
	dot := strings.Index(rest, ".")
	if dot < 0 {
		return qualified
	}
	return prefix + rest[:dot]
}

func inModule(path string) bool {
	return path == modulePath || strings.HasPrefix(path, modulePath+"/")
}

func hasInEdge(n *callgraph.Node) bool {
	return n != nil && len(n.In) > 0
}

// skippedPackage reports paths that are not part of the operator binary:
// test helpers, and Go files that npm packages happen to ship.
func skippedPackage(path string) bool {
	return strings.HasPrefix(path, modulePath+"/test/") || strings.Contains(path, "/node_modules/")
}

func qualifiedName(fn *ssa.Function) string {
	var buf strings.Builder
	if fn.Pkg != nil {
		buf.WriteString(fn.Pkg.Pkg.Path())
		buf.WriteByte('.')
	}
	writeFuncName(&buf, fn)
	return buf.String()
}

func writeFuncName(buf *strings.Builder, fn *ssa.Function) {
	if parent := fn.Parent(); parent != nil {
		writeFuncName(buf, parent)
		i := slices.Index(parent.AnonFuncs, fn)
		fmt.Fprintf(buf, "$%d", i+1)
		return
	}
	if recv := receiverNamed(fn); recv != nil {
		buf.WriteString(recv.Obj().Name())
		buf.WriteByte('.')
	}
	buf.WriteString(fn.Name())
}

func receiverNamed(fn *ssa.Function) *types.Named {
	sig := fn.Signature
	if sig == nil || sig.Recv() == nil {
		return nil
	}
	t := sig.Recv().Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := types.Unalias(t).(*types.Named)
	return named
}

// isMarkerMethod reports whether decl is an unexported empty method whose only
// job is to constrain which types implement an interface in the same package.
func isMarkerMethod(decl *ast.FuncDecl, fn *ssa.Function, interfaceTypes []*types.Interface) bool {
	if fn.Signature == nil || fn.Signature.Recv() == nil || ast.IsExported(fn.Name()) {
		return false
	}
	if params := fn.Signature.Params(); params != nil && params.Len() > 0 {
		return false
	}
	if results := fn.Signature.Results(); results != nil && results.Len() > 0 {
		return false
	}
	if decl.Body == nil || len(decl.Body.List) > 0 {
		return false
	}
	return slices.ContainsFunc(interfaceTypes, func(iface *types.Interface) bool {
		return types.Implements(fn.Signature.Recv().Type(), iface)
	})
}
