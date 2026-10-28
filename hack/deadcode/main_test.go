package main

import (
	"strings"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	list, err := parseAllowlist(`
# comment

github.com/argoproj-labs/gitops-promoter/internal/settings.SetControllerInstanceIDForTest
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.order) != 1 {
		t.Fatalf("entries = %d, want 1", len(list.order))
	}
	if list.order[0].line != 4 {
		t.Fatalf("line = %d, want 4", list.order[0].line)
	}

	_, err = parseAllowlist("pkg.A\npkg.A\n")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestIgnoredByName(t *testing.T) {
	if !ignoredByName("github.com/argoproj-labs/gitops-promoter/internal/settings.SetControllerInstanceIDForTest") {
		t.Fatal("ForTest suffix should be ignored")
	}
	if !ignoredByName("github.com/argoproj-labs/gitops-promoter/internal/controller.loadShippedControllerConfigurationForTests") {
		t.Fatal("ForTests suffix should be ignored")
	}
	if ignoredByName("github.com/argoproj-labs/gitops-promoter/api/v1alpha1.Environment.GetAutoMerge") {
		t.Fatal("GetAutoMerge should not be ignored by name")
	}
}

func TestKindOf(t *testing.T) {
	if got := kindOf(false, false); got != "unreachable" {
		t.Fatalf("unreachable = %q", got)
	}
	if got := kindOf(true, false); got != "reflection-only" {
		t.Fatalf("reflection-only = %q", got)
	}
	if got := kindOf(true, true); got != "" {
		t.Fatalf("live = %q", got)
	}
}

func TestSkippedPackage(t *testing.T) {
	if !skippedPackage(modulePath + "/test/scmserver") {
		t.Fatal("test helpers should be skipped")
	}
	if !skippedPackage(modulePath + "/ui/dashboard/node_modules/flatted/golang/pkg/flatted") {
		t.Fatal("node_modules should be skipped")
	}
	if skippedPackage(modulePath + "/internal/utils/httpauth") {
		t.Fatal("internal packages should be checked")
	}
	if skippedPackage(modulePath + "/cmd/demo") {
		t.Fatal("cmd should be checked")
	}
}
func TestPackagePath(t *testing.T) {
	got := packagePath("github.com/argoproj-labs/gitops-promoter/internal/controller.ChangeTransferPolicyReconciler.Reconcile")
	want := "github.com/argoproj-labs/gitops-promoter/internal/controller"
	if got != want {
		t.Fatalf("packagePath = %q, want %q", got, want)
	}
	if !inModule(got) {
		t.Fatal("controller package should be in the module")
	}
	if inModule("golang.org/x/tools/go/ssa") {
		t.Fatal("x/tools should be outside the module")
	}
}

func TestFindingFormat(t *testing.T) {
	unused := finding{
		file: "api/v1alpha1/promotionstrategy_types.go",
		line: 144,
		col:  1,
		kind: "reflection-only",
		name: "github.com/argoproj-labs/gitops-promoter/api/v1alpha1.Environment.GetAutoMerge",
	}.String()
	if !strings.Contains(unused, "reflection-only func: ") {
		t.Fatal(unused)
	}
	stale := finding{file: "hack/deadcode/allowlist.txt", line: 3, col: 1, kind: "stale allowlist entry", name: "pkg.Old"}.String()
	if strings.Contains(stale, " func:") {
		t.Fatal(stale)
	}
}
