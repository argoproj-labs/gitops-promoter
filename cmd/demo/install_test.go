package demo

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestArgoCDPatchArgsUseEmbeddedPatch(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("config/argocd-extension.yaml")
	if err != nil {
		t.Fatal(err)
	}

	args := argoCDPatchArgs()
	if slices.Contains(args, "--patch-file") {
		t.Fatalf("args reference a patch file relative to the working directory: %v", args)
	}
	idx := slices.Index(args, "--patch")
	if idx < 0 || idx+1 >= len(args) {
		t.Fatalf("args have no --patch value: %v", args)
	}
	if args[idx+1] != string(want) {
		t.Error("--patch value does not match config/argocd-extension.yaml")
	}
	if !strings.Contains(args[idx+1], "extension-gitops-promoter") {
		t.Error("--patch value does not contain the extension init container")
	}
}
