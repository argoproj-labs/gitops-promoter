package demo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCreateOrUpdateSecretReturnsNonNotFoundGetError(t *testing.T) {
	t.Parallel()

	clientset := fake.NewClientset()
	getErr := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "s", errors.New("denied"))
	clientset.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, getErr //nolint:wrapcheck // the fake API server returns the error as is
	})
	createCalled := false
	clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		createCalled = true
		return false, nil, nil
	})

	err := CreateOrUpdateSecret(context.Background(), clientset, "argocd", "s", map[string]string{"k": "v"}, nil)
	if !apierrors.IsForbidden(err) {
		t.Errorf("err = %v, want the Forbidden Get error", err)
	}
	if createCalled {
		t.Error("Create was attempted after a non-NotFound Get error")
	}
}

//nolint:paralleltest // replaces the package-level setupLog.
func TestCreateOrUpdateSecretLogsKeyValues(t *testing.T) {
	var logs []string
	original := setupLog
	setupLog = funcr.New(func(prefix, args string) { logs = append(logs, args) }, funcr.Options{})
	t.Cleanup(func() { setupLog = original })

	clientset := fake.NewClientset()
	ctx := context.Background()
	if err := CreateOrUpdateSecret(ctx, clientset, "argocd", "s", map[string]string{"k": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := CreateOrUpdateSecret(ctx, clientset, "argocd", "s", map[string]string{"k": "v2"}, nil); err != nil {
		t.Fatal(err)
	}

	if len(logs) != 2 {
		t.Fatalf("got %d log lines, want 2: %v", len(logs), logs)
	}
	for _, line := range logs {
		if strings.Contains(line, "%s") {
			t.Errorf("log line has printf verbs: %s", line)
		}
		if !strings.Contains(line, `"namespace"="argocd"`) || !strings.Contains(line, `"name"="s"`) {
			t.Errorf("log line lacks namespace/name key-value pairs: %s", line)
		}
	}
}
