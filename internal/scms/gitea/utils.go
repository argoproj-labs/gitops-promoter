package gitea

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"code.gitea.io/sdk/gitea"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func commitPhaseToGiteaStatusState(commitPhase promoterv1alpha1.CommitStatusPhase) (gitea.StatusState, error) {
	switch commitPhase {
	case promoterv1alpha1.CommitPhaseFailure:
		return gitea.StatusFailure, nil
	case promoterv1alpha1.CommitPhasePending:
		return gitea.StatusPending, nil
	case promoterv1alpha1.CommitPhaseSuccess:
		return gitea.StatusSuccess, nil
	default:
		return gitea.StatusError, fmt.Errorf("cannot map promoter commit phase '%v' to a gitea commit status", commitPhase)
	}
}

// ApplyHTTPAuth applies Gitea authentication to the HTTP request.
// It prefers "Authorization: token <token>" and falls back to HTTP Basic auth (username/password).
func ApplyHTTPAuth(secret corev1.Secret, req *http.Request) error {
	if token := string(secret.Data["token"]); token != "" {
		req.Header.Set("Authorization", "token "+token)
		return nil
	}
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username != "" && password != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+credentials)
		return nil
	}
	return errors.New("token or username/password required in secret for Gitea SCM auth")
}
