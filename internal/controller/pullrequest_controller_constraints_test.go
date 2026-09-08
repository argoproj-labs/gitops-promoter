/*
Copyright 2025 The Argoproj Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This test file is admittedly weird, since it's testing code construction rather than behavior.
// But the PullRequest controller can get really complex, and having some constraints around
// implementation keeps it maintainable.

const pullRequestControllerFile = "pullrequest_controller.go"

var pullRequestSCMMethods = []string{"FindOpen", "Get", "Merge", "Create", "Update", "Close"}

func parsePullRequestControllerAST() *ast.File {
	path := filepath.Join(".", pullRequestControllerFile)
	src, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "read %s", path)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	Expect(err).NotTo(HaveOccurred(), "parse %s", path)
	return f
}

func findFuncDecl(f *ast.File, name string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		return fn
	}
	return nil
}

func countProviderMethodCallsInSubtree(node ast.Node, method string) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "provider" || sel.Sel.Name != method {
			return true
		}
		count++
		return true
	})
	return count
}

func funcBodyAssignsPullRequestStatus(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			expr, ok := lhs.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			base, ok := expr.X.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pr, ok := base.X.(*ast.Ident)
			if !ok || pr.Name != "pr" {
				continue
			}
			if base.Sel.Name == "Status" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func exprReferencesMicrosecond(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return strings.Contains(e.Value, "Microsecond")
	case *ast.SelectorExpr:
		return e.Sel.Name == "Microsecond"
	case *ast.BinaryExpr:
		return exprReferencesMicrosecond(e.X) || exprReferencesMicrosecond(e.Y)
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Microsecond" {
			return true
		}
		return slices.ContainsFunc(e.Args, exprReferencesMicrosecond)
	default:
		return false
	}
}

func reconcileContainsMicroRequeue(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RequeueAfter" {
				return true
			}
			if slices.ContainsFunc(node.Args, exprReferencesMicrosecond) {
				found = true
				return false
			}
		case *ast.KeyValueExpr:
			ident, ok := node.Key.(*ast.Ident)
			if !ok || ident.Name != "RequeueAfter" {
				return true
			}
			if exprReferencesMicrosecond(node.Value) {
				found = true
				return false
			}
		default:
			return true
		}
		return true
	})
	return found
}

func collectReconcileSCMCallGraph(f *ast.File) map[string]*ast.FuncDecl {
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcs[fn.Name.Name] = fn
	}

	reachable := map[string]bool{"Reconcile": true}
	queue := []string{"Reconcile"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		fn := funcs[name]
		if fn == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "r" {
				callee := sel.Sel.Name
				if !reachable[callee] {
					reachable[callee] = true
					queue = append(queue, callee)
				}
			}
			return true
		})
	}

	subgraph := map[string]*ast.FuncDecl{}
	for name := range reachable {
		if fn := funcs[name]; fn != nil {
			subgraph[name] = fn
		}
	}
	return subgraph
}

func countProviderMethodCallsInFuncs(funcs map[string]*ast.FuncDecl, method string) int {
	count := 0
	for _, fn := range funcs {
		count += countProviderMethodCallsInSubtree(fn.Body, method)
	}
	return count
}

var _ = Describe("PullRequest controller structural constraints", func() {
	It("calls each SCM provider method from exactly one path in Reconcile", func() {
		f := parsePullRequestControllerAST()
		callGraph := collectReconcileSCMCallGraph(f)

		for _, method := range pullRequestSCMMethods {
			count := countProviderMethodCallsInFuncs(callGraph, method)
			Expect(count).To(Equal(1), "Reconcile call graph must call provider.%s exactly once", method)
		}
	})

	It("forbids micro RequeueAfter in Reconcile", func() {
		f := parsePullRequestControllerAST()
		reconcile := findFuncDecl(f, "Reconcile")
		Expect(reconcile).NotTo(BeNil())
		Expect(reconcileContainsMicroRequeue(reconcile)).To(BeFalse(),
			"Reconcile must not use RequeueAfter: 1 * time.Microsecond for chaining")
	})

	It("does not assign pr.Status in finalizer release helpers", func() {
		f := parsePullRequestControllerAST()
		for _, name := range []string{"releaseFinalizer"} {
			fn := findFuncDecl(f, name)
			Expect(fn).NotTo(BeNil(), "expected function %s", name)
			Expect(funcBodyAssignsPullRequestStatus(fn)).To(BeFalse(),
				"%s must not assign pr.Status", name)
		}
	})
})
