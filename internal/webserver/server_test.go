package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testMergeRequestBody = `{"namespace": "test-ns", "name": "test-pr"}`

// mockPullRequestProvider implements scms.PullRequestProvider for testing
type mockPullRequestProvider struct {
	mergeFunc func(ctx context.Context, pr promoterv1alpha1.PullRequest) error
}

func (m *mockPullRequestProvider) Create(ctx context.Context, title, head, base, description string, pullRequest promoterv1alpha1.PullRequest) (string, error) {
	return "", nil
}

func (m *mockPullRequestProvider) Close(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) error {
	return nil
}

func (m *mockPullRequestProvider) Update(ctx context.Context, title, description string, pullRequest promoterv1alpha1.PullRequest) error {
	return nil
}

func (m *mockPullRequestProvider) Merge(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) error {
	if m.mergeFunc != nil {
		return m.mergeFunc(ctx, pullRequest)
	}
	return nil
}

func (m *mockPullRequestProvider) FindOpen(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) (bool, string, time.Time, error) {
	return false, "", time.Time{}, nil
}

func (m *mockPullRequestProvider) GetUrl(ctx context.Context, pullRequest promoterv1alpha1.PullRequest) (string, error) {
	return "", nil
}

func TestHttpMerge_InvalidRequest(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	// Test missing namespace
	body := `{"name": "test-pr"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "invalid request")
}

func TestHttpMerge_PullRequestNotFound(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	body := `{"namespace": "test-ns", "name": "nonexistent-pr"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "pull request not found")
}

func TestHttpMerge_PullRequestNotOpen(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestMerged,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "test-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "123",
			State: promoterv1alpha1.PullRequestMerged,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "pull request is not open")
}

func TestHttpMerge_PullRequestNoID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestOpen,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "test-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "", // No ID yet
			State: promoterv1alpha1.PullRequestOpen,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "pull request has no ID")
}

func TestHttpMerge_MissingGitRepository(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestOpen,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "nonexistent-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "123",
			State: promoterv1alpha1.PullRequestOpen,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "failed to get SCM provider")
}

func TestHttpMerge_Success(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestOpen,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "test-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "123",
			State: promoterv1alpha1.PullRequestOpen,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	// Track if merge was called
	mergeCalled := false
	mockProvider := &mockPullRequestProvider{
		mergeFunc: func(ctx context.Context, pr promoterv1alpha1.PullRequest) error {
			mergeCalled = true
			return nil
		},
	}

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
		GetPullRequestProvider: func(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
			return mockProvider, nil
		},
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mergeCalled, "expected merge to be called on the SCM provider")

	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "merged", resp.State)
}

func TestHttpMerge_SCMProviderError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestOpen,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "test-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "123",
			State: promoterv1alpha1.PullRequestOpen,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	// Mock provider that returns an error on merge
	mockProvider := &mockPullRequestProvider{
		mergeFunc: func(ctx context.Context, pr promoterv1alpha1.PullRequest) error {
			return errors.New("merge conflict: branch is out of date")
		},
	}

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
		GetPullRequestProvider: func(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
			return mockProvider, nil
		},
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "failed to merge pull request")
	assert.Contains(t, resp.Message, "merge conflict")
}

func TestHttpMerge_ProviderCreationError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	scheme := runtime.NewScheme()
	require.NoError(t, promoterv1alpha1.AddToScheme(scheme))

	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			Title:        "Test PR",
			SourceBranch: "feature",
			TargetBranch: "main",
			State:        promoterv1alpha1.PullRequestOpen,
			MergeSha:     "abc123abc123abc123abc123abc123abc123abc1",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: "test-repo",
			},
		},
		Status: promoterv1alpha1.PullRequestStatus{
			ID:    "123",
			State: promoterv1alpha1.PullRequestOpen,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pr).
		Build()

	ws := WebServer{
		Client:              k8sClient,
		Scheme:              scheme,
		ControllerNamespace: "test-namespace",
		GetPullRequestProvider: func(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
			return nil, errors.New("failed to authenticate with SCM provider")
		},
	}

	router := gin.New()
	router.POST("/merge", ws.httpMerge)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/merge", bytes.NewBufferString(testMergeRequestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp mergeResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.State)
	assert.Contains(t, resp.Message, "failed to get SCM provider")
	assert.Contains(t, resp.Message, "failed to authenticate")
}
