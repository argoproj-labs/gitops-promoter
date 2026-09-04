package forgejo

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func commitPhaseToForgejoStatusState(commitPhase promoterv1alpha1.CommitStatusPhase) (forgejo.StatusState, error) {
	switch commitPhase {
	case promoterv1alpha1.CommitPhaseFailure:
		return forgejo.StatusFailure, nil
	case promoterv1alpha1.CommitPhasePending:
		return forgejo.StatusPending, nil
	case promoterv1alpha1.CommitPhaseSuccess:
		return forgejo.StatusSuccess, nil
	default:
		return forgejo.StatusError, fmt.Errorf("cannot map promoter commit phase '%v' to a forgejo commit status", commitPhase)
	}
}

// ApplyHTTPAuth applies Forgejo authentication to the HTTP request.
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
	return errors.New("token or username/password required in secret for Forgejo SCM auth")
}
