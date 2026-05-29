package controller_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file holds plain Go (non-Ginkgo) tests that verify each reconciler's
// SetupWithManager wires the InstanceID predicate exactly as required for
// ARGO-3085.
//
// The tests inspect controller source files with go/ast rather than spinning
// up controller-runtime managers. Two reasons:
//   1. controller-runtime does not expose the chained predicate after the
//      builder finishes, so behavioural inspection is impossible without a
//      real informer cache.
//   2. The single load-bearing invariant ("And, not Or") is a syntactic
//      property of the call site — AST inspection catches it deterministically
//      and instantly, with no envtest cost.
//
// If a reconciler's wiring drifts from the expected pattern, one of these
// tests will fail with a precise file:line citation.

const (
	helperPkgAlias       = "promoterpredicate"
	helperFunc           = "InstanceID"
	crdPromotionStrategy = "PromotionStrategy"
)

// expectedWiring captures, per reconciler file, the syntactic predicate-chain
// expectations the implementer was supposed to land. The check is intentionally
// strict: a single Or-wraps-InstanceID would flip the semantics from filter to
// noop, so we assert the canonical And-shape at every site.
type expectedWiring struct {
	// file is the controller filename under internal/controller/.
	file string
	// forCRD is the type literal expected inside the reconciler's For(...) call
	// (e.g. "PullRequest"). Used to identify the For invocation; the test then
	// asserts the WithPredicates argument contains an And() with an InstanceID
	// leaf that does NOT live under an Or().
	forCRD string
	// watchesWithInstanceID lists the watched type literals that must also
	// carry the InstanceID predicate (e.g. "PromotionStrategy" for the three
	// timed/git/web reconcilers, plus ArgoCDCommitStatus option-b).
	watchesWithInstanceID []string
	// watchesWithoutInstanceID lists watched types that must NOT carry the
	// InstanceID predicate. ArgoCDCommitStatus option-b explicitly leaves
	// remote-cluster Application watches unfiltered.
	watchesWithoutInstanceID []string
}

var simpleReconcilers = []expectedWiring{
	{file: "pullrequest_controller.go", forCRD: "PullRequest"},
	{file: "revertcommit_controller.go", forCRD: "RevertCommit"},
	{file: "commitstatus_controller.go", forCRD: "CommitStatus"},
	{file: "promotionstrategy_controller.go", forCRD: crdPromotionStrategy},
	{file: "scmprovider_controller.go", forCRD: "ScmProvider"},
	{file: "gitrepository_controller.go", forCRD: "GitRepository"},
	{file: "controllerconfiguration_controller.go", forCRD: "ControllerConfiguration"},
	{file: "clusterscmprovider_controller.go", forCRD: "ClusterScmProvider"},
	{
		file:                  "timedcommitstatus_controller.go",
		forCRD:                "TimedCommitStatus",
		watchesWithInstanceID: []string{crdPromotionStrategy},
	},
	{
		file:                  "gitcommitstatus_controller.go",
		forCRD:                "GitCommitStatus",
		watchesWithInstanceID: []string{crdPromotionStrategy},
	},
	{
		file:                  "webrequestcommitstatus_controller.go",
		forCRD:                "WebRequestCommitStatus",
		watchesWithInstanceID: []string{crdPromotionStrategy},
	},
}

func TestSimpleReconcilersChainInstanceIDPredicate(t *testing.T) {
	t.Parallel()
	for _, wiring := range simpleReconcilers {
		t.Run(wiring.file, func(t *testing.T) {
			t.Parallel()
			assertReconcilerWiring(t, wiring)
		})
	}
}

func TestChangeTransferPolicyWiring(t *testing.T) {
	t.Parallel()

	wiring := expectedWiring{
		file:   "changetransferpolicy_controller.go",
		forCRD: "ChangeTransferPolicy",
	}
	assertReconcilerWiring(t, wiring)

	// CTP also enforces the filter at the channel-sender side because
	// WatchesRawSource(Channel) bypasses controller-runtime predicates.
	src := loadControllerSource(t, wiring.file)
	if !strings.Contains(src, "r.InstanceID != \"\"") {
		t.Error("changetransferpolicy_controller.go: expected sender-side instance-id gate `r.InstanceID != \"\"` inside enqueueFunc; not found. Without it WatchesRawSource(Channel) events leak across instances.")
	}
	if !strings.Contains(src, "promoterv1alpha1.InstanceIDLabel") {
		t.Error("changetransferpolicy_controller.go: expected reference to promoterv1alpha1.InstanceIDLabel inside enqueueFunc; not found.")
	}
}

func TestArgoCDCommitStatusOptionB(t *testing.T) {
	t.Parallel()

	wiring := expectedWiring{
		file:                     "argocdcommitstatus_controller.go",
		forCRD:                   "ArgoCDCommitStatus",
		watchesWithInstanceID:    []string{crdPromotionStrategy},
		watchesWithoutInstanceID: []string{"Application"},
	}
	assertReconcilerWiring(t, wiring)
}

// assertReconcilerWiring parses the controller file and verifies, via AST
// inspection, that the SetupWithManager body matches the wiring contract:
//   - For(&promoterv1alpha1.<forCRD>{}, ...) carries WithPredicates(predicate.And(...))
//     and the And contains promoterpredicate.InstanceID(...) NOT under a sibling
//     predicate.Or call.
//   - Every type listed in watchesWithInstanceID has a Watches(...) call whose
//     WithPredicates argument either is promoterpredicate.InstanceID(...) directly
//     or includes it as an And leaf.
//   - Every type listed in watchesWithoutInstanceID has a Watches(...) call whose
//     WithPredicates does NOT mention promoterpredicate.InstanceID.
func assertReconcilerWiring(t *testing.T, w expectedWiring) {
	t.Helper()

	path := filepath.Join(".", w.file)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	setup := findSetupWithManager(file)
	if setup == nil {
		t.Fatalf("%s: SetupWithManager function not found", w.file)
	}

	forCall, watchCalls := collectBuilderCalls(setup)

	if forCall == nil {
		t.Fatalf("%s: no For(...) call found in SetupWithManager", w.file)
	}
	if !forCallTargets(forCall, w.forCRD) {
		t.Fatalf("%s: For(...) call did not target promoterv1alpha1.%s", w.file, w.forCRD)
	}

	forPredicates := withPredicatesArg(forCall)
	if forPredicates == nil {
		t.Fatalf("%s: For(...) for %s does not pass builder.WithPredicates(...)", w.file, w.forCRD)
	}
	if !containsInstanceIDViaAnd(forPredicates) {
		t.Errorf("%s: For(%s).WithPredicates did not reach promoterpredicate.InstanceID via predicate.And — %s",
			w.file, w.forCRD, predicateChainHint(forPredicates, fset))
	}
	if containsInstanceIDUnderOr(forPredicates) {
		t.Errorf("%s: For(%s).WithPredicates places promoterpredicate.InstanceID inside a predicate.Or — Or short-circuits to allow events on any matching predicate, defeating the instance filter.",
			w.file, w.forCRD)
	}

	for _, watchType := range w.watchesWithInstanceID {
		call := findWatchCall(watchCalls, watchType)
		if call == nil {
			t.Errorf("%s: expected a Watches(%s, ...) call, not found", w.file, watchType)
			continue
		}
		preds := withPredicatesArg(call)
		if preds == nil {
			t.Errorf("%s: Watches(%s) does not pass WithPredicates(...)", w.file, watchType)
			continue
		}
		if !mentionsInstanceID(preds) {
			t.Errorf("%s: Watches(%s).WithPredicates does not mention promoterpredicate.InstanceID — cross-instance %s events would enqueue work in the wrong wave.",
				w.file, watchType, watchType)
		}
		if containsInstanceIDUnderOr(preds) {
			t.Errorf("%s: Watches(%s).WithPredicates places promoterpredicate.InstanceID inside a predicate.Or — Or defeats the instance filter.",
				w.file, watchType)
		}
	}

	for _, watchType := range w.watchesWithoutInstanceID {
		call := findWatchCall(watchCalls, watchType)
		if call == nil {
			// Acceptable: the watch may not exist in this controller. We only
			// fail if it IS there and erroneously carries InstanceID (option-b
			// says remote Application watches must remain unfiltered).
			continue
		}
		preds := withPredicatesArg(call)
		if preds != nil && mentionsInstanceID(preds) {
			t.Errorf("%s: Watches(%s).WithPredicates incorrectly applies promoterpredicate.InstanceID — option-b (Michael Crenshaw 2026-05-21) keeps remote-cluster %s watches unfiltered because remote objects do not carry our label.",
				w.file, watchType, watchType)
		}
	}
}

// findSetupWithManager returns the *ast.FuncDecl for SetupWithManager, or nil
// if absent.
func findSetupWithManager(file *ast.File) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name.Name != "SetupWithManager" {
			continue
		}
		if fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		return fn
	}
	return nil
}

// collectBuilderCalls walks SetupWithManager and returns the For(...) call
// plus every Watches(...) call. Builder chains like
// `ctrl.NewControllerManagedBy(mgr).For(...).Watches(...).Watches(...).Complete(r)`
// are parsed as nested SelectorExpr/CallExpr; the helper flattens them.
func collectBuilderCalls(fn *ast.FuncDecl) (forCall *ast.CallExpr, watchCalls []*ast.CallExpr) {
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "For":
			forCall = call
		case "Watches":
			watchCalls = append(watchCalls, call)
		default:
			// Other builder calls (e.g. WithEventFilter, Owns, Complete) are
			// not relevant for this assertion.
		}
		return true
	})
	return forCall, watchCalls
}

// forCallTargets reports whether the For(...) call's first arg is
// &promoterv1alpha1.<crd>{}.
func forCallTargets(call *ast.CallExpr, crd string) bool {
	if len(call.Args) == 0 {
		return false
	}
	return isPromoterTypeLiteral(call.Args[0], crd)
}

// findWatchCall returns the Watches(...) call whose first argument is the
// promoterv1alpha1.<type>{} (or argocd.Application{}) literal. The match is
// by the type's final identifier so "Application" matches argocd.Application
// regardless of package alias.
func findWatchCall(calls []*ast.CallExpr, watchType string) *ast.CallExpr {
	for _, call := range calls {
		if len(call.Args) == 0 {
			continue
		}
		if isTypeLiteralByName(call.Args[0], watchType) {
			return call
		}
	}
	return nil
}

// isPromoterTypeLiteral reports whether expr is &promoterv1alpha1.<typ>{}.
func isPromoterTypeLiteral(expr ast.Expr, typ string) bool {
	unary, ok := expr.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return false
	}
	composite, ok := unary.X.(*ast.CompositeLit)
	if !ok {
		return false
	}
	sel, ok := composite.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "promoterv1alpha1" && sel.Sel.Name == typ
}

// isTypeLiteralByName matches &pkg.<typ>{} regardless of package identifier,
// so callers can target either "PromotionStrategy" (in promoterv1alpha1) or
// "Application" (in the argocd alias) without listing every alias.
func isTypeLiteralByName(expr ast.Expr, typ string) bool {
	unary, ok := expr.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return false
	}
	composite, ok := unary.X.(*ast.CompositeLit)
	if !ok {
		return false
	}
	sel, ok := composite.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == typ
}

// withPredicatesArg looks for a sibling-call WithPredicates(predicateExpr) in
// the variadic options of a For/Watches call and returns the predicateExpr.
// It accepts both `builder.WithPredicates(...)` and
// `mcbuilder.WithPredicates(...)` (multicluster builder).
func withPredicatesArg(call *ast.CallExpr) ast.Expr {
	for _, arg := range call.Args[1:] {
		inner, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if sel.Sel.Name != "WithPredicates" {
			continue
		}
		if len(inner.Args) == 0 {
			continue
		}
		return inner.Args[0]
	}
	return nil
}

// mentionsInstanceID returns true if expr (or anything inside it) is a call to
// promoterpredicate.InstanceID(...) OR is an identifier that aliases such a
// call (the ArgoCDCommitStatus controller assigns
// `instanceIDPredicate := promoterpredicate.InstanceID(r.InstanceID)` and
// references the local later).
func mentionsInstanceID(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isInstanceIDCall(x) {
				found = true
				return false
			}
		case *ast.Ident:
			// Local variable aliases used in ArgoCDCommitStatus.
			if x.Name == "instanceIDPredicate" {
				found = true
				return false
			}
		default:
			// Other AST nodes don't carry an instance-id reference.
		}
		return true
	})
	return found
}

// containsInstanceIDViaAnd returns true if expr is a predicate.And(...) call
// (or contains one) that has promoterpredicate.InstanceID reachable as a
// direct argument.
func containsInstanceIDViaAnd(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "And" {
			return true
		}
		for _, arg := range call.Args {
			if exprMentionsInstanceID(arg) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// exprMentionsInstanceID is mentionsInstanceID but scoped to a single
// expression (no recursion into builder calls). Used inside the And.
func exprMentionsInstanceID(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.CallExpr:
		if isInstanceIDCall(x) {
			return true
		}
	case *ast.Ident:
		if x.Name == "instanceIDPredicate" {
			return true
		}
	default:
		// Other expression kinds don't reference instance-id directly.
	}
	return false
}

// containsInstanceIDUnderOr returns true if expr contains a predicate.Or call
// whose subtree mentions promoterpredicate.InstanceID. Or-wrapping the
// instance predicate would make it pass whenever the OTHER predicate passes,
// defeating the filter — this MUST never appear.
func containsInstanceIDUnderOr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "Or" {
			return true
		}
		for _, arg := range call.Args {
			if exprMentionsInstanceID(arg) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isInstanceIDCall reports whether call is promoterpredicate.InstanceID(...).
func isInstanceIDCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != helperFunc {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == helperPkgAlias
}

// predicateChainHint renders a human-readable snippet of the predicate
// expression for the error message so a failure points the developer at the
// exact wiring shape.
func predicateChainHint(expr ast.Expr, fset *token.FileSet) string {
	pos := fset.Position(expr.Pos())
	return "see " + pos.String()
}

// loadControllerSource reads a controller source file relative to this test
// file's directory.
func loadControllerSource(t *testing.T, filename string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(".", filename))
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return string(data)
}
